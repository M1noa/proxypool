// package history ports lib/history.py: a duckdb-backed proxy_state table
// tracking per-protocol reliability/speed with ema blending, plus the
// skip-selection that lets check_all skip re-probing proxies with a recent
// failure streak.
package history

import (
	"database/sql"
	"math"
	"math/rand"
	"strconv"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"

	"github.com/M1noa/proxypool/internal/check"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

const (
	emaAlpha      = 2.0 / 169.0 // ~168-check effective window (old RECENT_WINDOW)
	recentWeight  = 0.7         // blend: 0.7*ema + 0.3*all-time
	rtGoodMS      = 500.0       // quality speed factor 1.0 at or below this
	rtBadMS       = 8000.0      // quality speed factor 0.0 at or above this
	NewProxyScore = 0.5         // neutral score for proxies with no history

	skipFailStreak    = 24   // streak where skip probability maxes out
	recheckEveryHours = 24.0 // hard floor: never skip if unchecked this long
	baseSkipProb      = 0.15 // skip chance after a single failed check
	maxSkipProb       = 0.9  // always leave a 10% recheck chance

	pruneDays = 14 // drop proxies not checked within this window, every run

	tsFmt         = "2006-01-02T15:04:05Z"
	schemaVersion = 3
)

const schemaSQL = `CREATE TABLE IF NOT EXISTS proxy_state (
    ip              VARCHAR NOT NULL,
    port            INTEGER NOT NULL,
    proto           VARCHAR NOT NULL,
    fails_streak    INTEGER NOT NULL DEFAULT 0,
    last_ok_ts      VARCHAR,
    last_checked_ts VARCHAR,
    first_seen_ts   VARCHAR,
    rel_ema         DOUBLE,
    rt_ema          DOUBLE,
    ok_count        INTEGER NOT NULL DEFAULT 0,
    check_count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ip, port, proto)
)`

// Key identifies one proxy_state row, the same (ip, port, proto) tuple
// check.SkipKey uses.
type Key struct {
	IP    string
	Port  int
	Proto string
}

// State is one proxy_state row's history, as read back by StateMap.
type State struct {
	FailsStreak   int
	LastOKTS      *string
	LastCheckedTS *string
	FirstSeenTS   *string
	RelEMA        *float64
	RTEMA         *float64
	OKCount       int
	CheckCount    int
}

// Score is one (ip, port, proto)'s blended reliability/quality plus
// lifetime counters, as returned by Scores.
type Score struct {
	Reliability float64
	Quality     float64
	ChecksTotal int
	ChecksOK    int
	FirstSeen   *string
	LastSeen    *string
}

// History wraps output/history.duckdb.
type History struct {
	db *sql.DB
}

// Open is History.__init__: connect and ensure proxy_state exists, dropping
// and recreating it whenever the stored schema_version doesn't match —
// history is derived data, safe to discard and rebuild from a fresh run.
func Open(path string) (*History, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	h := &History{db: db}
	if err := h.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return h, nil
}

func (h *History) migrate() error {
	if _, err := h.db.Exec(
		"CREATE TABLE IF NOT EXISTS meta (k VARCHAR PRIMARY KEY, v VARCHAR)"); err != nil {
		return err
	}

	var v string
	err := h.db.QueryRow(
		"SELECT v FROM meta WHERE k = 'schema_version'").Scan(&v)
	if err == nil && v == strconv.Itoa(schemaVersion) {
		_, err = h.db.Exec(schemaSQL)
		return err
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if _, err := h.db.Exec("DROP TABLE IF EXISTS proxy_state"); err != nil {
		return err
	}
	if _, err := h.db.Exec(schemaSQL); err != nil {
		return err
	}
	_, err = h.db.Exec(
		"INSERT OR REPLACE INTO meta (k, v) VALUES ('schema_version', ?)",
		strconv.Itoa(schemaVersion))
	return err
}

// Close closes the underlying connection.
func (h *History) Close() error { return h.db.Close() }

// StateMap is state_map: every proxy_state row, keyed by (ip, port, proto).
func (h *History) StateMap() (map[Key]State, error) {
	rows, err := h.db.Query(
		"SELECT ip, port, proto, fails_streak, last_ok_ts, last_checked_ts," +
			" first_seen_ts, rel_ema, rt_ema, ok_count, check_count FROM proxy_state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[Key]State{}
	for rows.Next() {
		var k Key
		var st State
		var lastOK, lastChecked, firstSeen sql.NullString
		var relEMA, rtEMA sql.NullFloat64
		if err := rows.Scan(&k.IP, &k.Port, &k.Proto, &st.FailsStreak,
			&lastOK, &lastChecked, &firstSeen, &relEMA, &rtEMA,
			&st.OKCount, &st.CheckCount); err != nil {
			return nil, err
		}
		if lastOK.Valid {
			st.LastOKTS = &lastOK.String
		}
		if lastChecked.Valid {
			st.LastCheckedTS = &lastChecked.String
		}
		if firstSeen.Valid {
			st.FirstSeenTS = &firstSeen.String
		}
		if relEMA.Valid {
			st.RelEMA = &relEMA.Float64
		}
		if rtEMA.Valid {
			st.RTEMA = &rtEMA.Float64
		}
		out[k] = st
	}
	return out, rows.Err()
}

// UpdateState is update_state: fold this run's outcomes into proxy_state.
// prev is the state read before this run (StateMap's result from before
// check.CheckAll ran), not a fresh read — the caller passes the same map it
// used to compute skip_keys.
func (h *History) UpdateState(outcomes []check.Outcome, ts string, prev map[Key]State) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		"INSERT INTO proxy_state (ip, port, proto, fails_streak, last_ok_ts," +
			" last_checked_ts, first_seen_ts, rel_ema, rt_ema, ok_count, check_count)" +
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)" +
			" ON CONFLICT (ip, port, proto) DO UPDATE SET" +
			" fails_streak = excluded.fails_streak," +
			" last_ok_ts = excluded.last_ok_ts," +
			" last_checked_ts = excluded.last_checked_ts," +
			" rel_ema = excluded.rel_ema," +
			" rt_ema = excluded.rt_ema," +
			" ok_count = excluded.ok_count," +
			" check_count = excluded.check_count")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, o := range outcomes {
		st := prev[Key{o.IP, o.Port, o.Proto}]

		streak := 0
		if !o.Alive {
			streak = st.FailsStreak + 1
		}

		lastOK := st.LastOKTS
		if o.Alive {
			lastOK = &ts
		}

		alive := 0.0
		if o.Alive {
			alive = 1.0
		}
		var rel float64
		if st.RelEMA == nil {
			rel = alive
		} else {
			rel = *st.RelEMA + emaAlpha*(alive-*st.RelEMA)
		}

		// rt_ema only updates on a successful probe with a measured time;
		// a dead or timed-out probe leaves the prior estimate untouched.
		rtEMA := st.RTEMA
		if o.Alive && o.RT != nil {
			r := float64(*o.RT)
			if st.RTEMA == nil {
				rtEMA = &r
			} else {
				v := *st.RTEMA + emaAlpha*(r-*st.RTEMA)
				rtEMA = &v
			}
		}

		firstSeen := ts
		if st.FirstSeenTS != nil && *st.FirstSeenTS != "" {
			firstSeen = *st.FirstSeenTS
		}

		okCount := st.OKCount
		if o.Alive {
			okCount++
		}

		if _, err := stmt.Exec(o.IP, o.Port, o.Proto, streak, lastOK, ts,
			firstSeen, rel, rtEMA, okCount, st.CheckCount+1); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Scores is scores: every proto's blended reliability/quality, skipping rows
// with zero checks (shouldn't happen, since check_count only ever
// increments, but python guards it the same way).
func (h *History) Scores() (map[Key]Score, error) {
	rows, err := h.db.Query(
		"SELECT ip, port, proto, rel_ema, rt_ema, ok_count, check_count," +
			" first_seen_ts, last_checked_ts FROM proxy_state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[Key]Score{}
	for rows.Next() {
		var k Key
		var relEMA, rtEMA sql.NullFloat64
		var ok, total int
		var first, last sql.NullString
		if err := rows.Scan(&k.IP, &k.Port, &k.Proto, &relEMA, &rtEMA,
			&ok, &total, &first, &last); err != nil {
			return nil, err
		}
		if total == 0 {
			continue
		}

		relAll := float64(ok) / float64(total)
		relEMAv := 0.0
		if relEMA.Valid {
			relEMAv = relEMA.Float64
		}
		rel := recentWeight*relEMAv + (1-recentWeight)*relAll

		speed := NewProxyScore
		if rtEMA.Valid {
			speed = math.Min(1.0, math.Max(0.0,
				1.0-(rtEMA.Float64-rtGoodMS)/(rtBadMS-rtGoodMS)))
		}

		sc := Score{
			Reliability: pyfmt.RoundN(rel, 4),
			Quality:     pyfmt.RoundN(rel*speed, 4),
			ChecksTotal: total,
			ChecksOK:    ok,
		}
		if first.Valid {
			sc.FirstSeen = &first.String
		}
		if last.Valid {
			sc.LastSeen = &last.String
		}
		out[k] = sc
	}
	return out, rows.Err()
}

// Prune is prune: drop rows not checked within pruneDays of now, returning
// the number of rows removed. now defaults to time.Now() when zero.
func (h *History) Prune(now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.UTC().AddDate(0, 0, -pruneDays).Format(tsFmt)

	var n int
	if err := h.db.QueryRow(
		"SELECT count(*) FROM proxy_state WHERE last_checked_ts < ?", cutoff,
	).Scan(&n); err != nil {
		return 0, err
	}
	if _, err := h.db.Exec(
		"DELETE FROM proxy_state WHERE last_checked_ts < ?", cutoff); err != nil {
		return 0, err
	}
	return n, nil
}

// parseTS is parse_ts.
func parseTS(s string) (time.Time, error) {
	return time.Parse(tsFmt, s)
}

// SkipKeys is skip_keys: pick which (ip, port, proto) probes to skip this
// run, weighted by how long a protocol has been failing, but never past
// recheckEveryHours since its last check. rng defaults to a fresh
// math/rand.Rand seeded from the current time when nil; tests pass a
// deterministic one.
func SkipKeys(records []*extract.Record, state map[Key]State, now time.Time, rng *rand.Rand) map[check.SkipKey]bool {
	if now.IsZero() {
		now = time.Now()
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	skips := map[check.SkipKey]bool{}
	for _, r := range records {
		claimed := r.Protocols
		if len(claimed) == 0 {
			claimed = []string{"http", "https"}
		}
		for _, proto := range claimed {
			if proto == "" {
				continue
			}
			st, ok := state[Key{r.IP, r.Port, proto}]
			if !ok {
				continue
			}
			streak := st.FailsStreak
			if streak < 1 {
				continue
			}
			if st.LastCheckedTS != nil {
				last, err := parseTS(*st.LastCheckedTS)
				if err == nil {
					hours := now.UTC().Sub(last).Hours()
					if hours >= recheckEveryHours {
						continue
					}
				}
			}
			p := baseSkipProb + (maxSkipProb-baseSkipProb)*math.Min(1.0,
				float64(streak-1)/(skipFailStreak-1))
			if rng.Float64() < p {
				skips[check.SkipKey{IP: r.IP, Port: r.Port, Proto: proto}] = true
			}
		}
	}
	return skips
}

// AliveMap builds check.Options.PrevAlive: every (ip, port) with any
// protocol that has ever answered, matching fetch_proxies.py's
// `{k[:2] for k, v in state.items() if v.get("last_ok_ts")}`.
func AliveMap(state map[Key]State) map[check.AliveKey]bool {
	out := map[check.AliveKey]bool{}
	for k, st := range state {
		if st.LastOKTS != nil {
			out[check.AliveKey{IP: k.IP, Port: k.Port}] = true
		}
	}
	return out
}

// Now returns the current time formatted the way every proxy_state
// timestamp column expects, matching
// `datetime.now(timezone.utc).strftime(TS_FMT)`.
func Now() string {
	return time.Now().UTC().Format(tsFmt)
}
