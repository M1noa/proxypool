// package history ports lib/history.py: a duckdb-backed proxy_state table
// tracking per-protocol reliability/speed with ema blending, plus the
// skip-selection that lets check_all skip re-probing proxies with a recent
// failure streak.
package history

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcboeker/go-duckdb/v2"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/M1noa/proxypool/internal/check"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

const (
	emaAlpha      = 2.0 / 169.0 // ~168-check window (old RECENT_WINDOW); see warmAlpha
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
	schemaVersion = 4
)

// stageBatch is how many rows get appended into staging before a flush, and it
// is the only bound on duckdb's memory here: a staged row pins buffer-manager
// blocks the appender holds and the buffer manager therefore cannot evict, so
// staging a whole run at once needs memory proportional to the run — measured
// failing at 1.74m rows under a 534mb limit. four flushes for a 2m-outcome run
// keeps the peak flat without making the delete below run often enough to
// matter. a var only so the multi-batch test can lower it.
var stageBatch = 500_000

// schemaSQL deliberately declares no primary key. duckdb persists the ART index
// a PRIMARY KEY implies, and on a real run's ~1.9m rows that index measured
// 122mb of a 139mb file — past github's 100mb blob limit, which is what the
// workflow force-pushes this file through. the same rows without it are 17mb.
// nothing needs the index: nextState computes each row in full from the prior
// state map, so the flush below is a set-based delete-then-insert rather than an
// upsert duckdb has to resolve, and uniqueness holds by construction.
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
    check_count     INTEGER NOT NULL DEFAULT 0
)`

// stageDDL mirrors proxy_state's columns, plus seq — the order rows were
// appended in, which flushSQL uses to make the last outcome for a key win the
// way a row-at-a-time loop did. it is a TEMP table so a run's worth of staging
// never lands in the history.duckdb the workflow publishes.
const stageDDL = `CREATE OR REPLACE TEMP TABLE state_stage (
    seq             BIGINT,
    ip              VARCHAR,
    port            INTEGER,
    proto           VARCHAR,
    fails_streak    INTEGER,
    last_ok_ts      VARCHAR,
    last_checked_ts VARCHAR,
    first_seen_ts   VARCHAR,
    rel_ema         DOUBLE,
    rt_ema          DOUBLE,
    ok_count        INTEGER,
    check_count     INTEGER
)`

// deleteSQL and insertSQL fold a staging batch into proxy_state: drop whatever
// the batch supersedes, then append the batch. two set-based statements per
// batch, so a run is a handful of statements rather than one per row.
//
// the QUALIFY is insurance, not correctness: merge() dedupes by ip:port and a
// probe plan holds each proto once, so a key cannot repeat within a batch. a key
// spanning two batches needs no special handling — the later batch's delete
// removes the earlier batch's row, which is the same last-write-wins the QUALIFY
// gives inside one.
const deleteSQL = `DELETE FROM proxy_state WHERE (ip, port, proto) IN
    (SELECT ip, port, proto FROM state_stage)`

const insertSQL = `INSERT INTO proxy_state
    SELECT ip, port, proto, fails_streak, last_ok_ts, last_checked_ts,
           first_seen_ts, rel_ema, rt_ema, ok_count, check_count
    FROM state_stage
    QUALIFY row_number() OVER (PARTITION BY ip, port, proto ORDER BY seq DESC) = 1`

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
	// a zero-byte file is not a database duckdb will open. the workflow's
	// restore step leaves one behind when the download 404s, so treat it the
	// same as no file at all rather than aborting the run.
	if st, err := os.Stat(path); err == nil && st.Size() == 0 {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("duckdb", path+memLimitDSN())
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

// memLimitDSN caps duckdb at half the ram that is actually free. left alone it
// defaults to 80% of *total* ram, which assumes it owns the machine — but the
// record set, the prior state and this run's outcomes are all still resident in
// the go heap when UpdateState runs, so that default overcommits by exactly the
// amount the go side is holding.
//
// the cap does not make duckdb spill: blocks an appender is holding cannot be
// evicted, so hitting it raises "could not allocate block" instead. that is the
// point. a readable error beats the kernel killing the whole job, which is what
// the uncapped default bought at 12.4gb. stageBatch is what keeps the peak under
// the cap in the first place.
func memLimitDSN() string {
	vm, err := mem.VirtualMemory()
	if err != nil || vm.Available == 0 {
		return ""
	}
	// a batched run measures well under 512mb of duckdb, so the floor is
	// headroom: it only matters on a machine too small for the default to be safe
	mb := max(vm.Available/2/(1<<20), 512)
	return fmt.Sprintf("?memory_limit=%dMB", mb)
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

// warmAlpha is emaAlpha widened for a proxy that has not been checked enough
// times to fill the window emaAlpha assumes. at the fixed 2/169 a young proxy
// never escapes its first outcome: the first check seeds the ema at exactly 0 or
// 1, and every later one closes 1.18% of the gap, so a proxy that failed once
// and then succeeded six times reports 0.069 reliability against an honest
// 6/7. 1/n makes the ema exactly the running mean of the alive indicator until
// the fixed window becomes the shorter memory of the two, at n=85.
//
// this matters because the db holds a median of 5 checks per proxy, not 168.
func warmAlpha(n int) float64 {
	if n < 1 {
		return 1
	}
	return math.Max(emaAlpha, 1/float64(n))
}

// nextState folds one outcome into a proxy's prior state. split out of the
// append loop so the ema arithmetic is testable without a database.
func nextState(o check.Outcome, ts string, st State) State {
	next := State{
		LastCheckedTS: &ts,
		LastOKTS:      st.LastOKTS,
		RTEMA:         st.RTEMA,
		OKCount:       st.OKCount,
		CheckCount:    st.CheckCount + 1,
	}
	if !o.Alive {
		next.FailsStreak = st.FailsStreak + 1
	}

	alive := 0.0
	if o.Alive {
		alive = 1.0
		next.LastOKTS = &ts
		next.OKCount++
	}

	rel := alive
	if st.RelEMA != nil {
		rel = *st.RelEMA + warmAlpha(next.CheckCount)*(alive-*st.RelEMA)
	}
	next.RelEMA = &rel

	// rt_ema only updates on a successful probe with a measured time; a dead or
	// timed-out probe leaves the prior estimate untouched. it warms on ok_count
	// rather than check_count because that is how many times it has been fed.
	if o.Alive && o.RT != nil {
		r := float64(*o.RT)
		if st.RTEMA != nil {
			r = *st.RTEMA + warmAlpha(next.OKCount)*(r-*st.RTEMA)
		}
		next.RTEMA = &r
	}

	next.FirstSeenTS = &ts
	if st.FirstSeenTS != nil && *st.FirstSeenTS != "" {
		next.FirstSeenTS = st.FirstSeenTS
	}
	return next
}

// UpdateState is update_state: fold this run's outcomes into proxy_state.
// prev is the state read before this run (StateMap's result from before
// check.CheckAll ran), not a fresh read — the caller passes the same map it
// used to compute skip_keys.
//
// the rows are bulk-appended into a staging table and folded in set-based, in
// batches, which is what python's single executemany call got for free. a
// prepared-statement loop is not a slower version of this, it is a different
// algorithm: duckdb is columnar and pins a fresh 256 KiB block per single-row
// statement for as long as the transaction is open, so ~1m upserts cost ~250gb
// and die. measured at 1m rows: 1.4s and 746mb, against an OOM at row 15823
// under a 4gb cap.
func (h *History) UpdateState(outcomes []check.Outcome, ts string, prev map[Key]State) error {
	if len(outcomes) == 0 {
		return nil
	}
	ctx := context.Background()
	// a TEMP table belongs to one connection and database/sql pools them, so
	// the staging DDL, the appender and the flush all have to share a pinned one
	conn, err := h.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for start := 0; start < len(outcomes); start += stageBatch {
		if err := flushBatch(ctx, conn, outcomes[start:min(start+stageBatch, len(outcomes))], ts, prev); err != nil {
			return err
		}
	}
	return nil
}

// flushBatch stages one batch and folds it in. the staging table is recreated
// per batch, which is also what discards the previous one's rows.
func flushBatch(ctx context.Context, conn *sql.Conn, batch []check.Outcome, ts string, prev map[Key]State) error {
	if _, err := conn.ExecContext(ctx, stageDDL); err != nil {
		return err
	}

	var app *duckdb.Appender
	if err := conn.Raw(func(dc any) (err error) {
		app, err = duckdb.NewAppender(dc.(driver.Conn), "temp", "main", "state_stage")
		return
	}); err != nil {
		return err
	}

	err := appendStates(app, batch, ts, prev)
	// Close is what flushes the final chunk into the staging table, so it has to
	// run even on an append failure — otherwise the rows already appended are
	// stranded in a chunk nothing will ever read.
	if cerr := app.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, deleteSQL); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, insertSQL)
	return err
}

// appendStates writes one row per outcome into the staging table. duckdb's
// appender is strictly typed, so INTEGER columns take int32 and a nil pointer
// has to become an untyped nil rather than a typed one.
func appendStates(app *duckdb.Appender, outcomes []check.Outcome, ts string, prev map[Key]State) error {
	for i, o := range outcomes {
		st := nextState(o, ts, prev[Key{o.IP, o.Port, o.Proto}])
		if err := app.AppendRow(
			int64(i), o.IP, int32(o.Port), o.Proto, int32(st.FailsStreak),
			nullStr(st.LastOKTS), nullStr(st.LastCheckedTS), nullStr(st.FirstSeenTS),
			nullF64(st.RelEMA), nullF64(st.RTEMA),
			int32(st.OKCount), int32(st.CheckCount),
		); err != nil {
			return fmt.Errorf("staging row %d (%s:%d/%s): %w", i, o.IP, o.Port, o.Proto, err)
		}
	}
	return nil
}

func nullStr(p *string) driver.Value {
	if p == nil {
		return nil
	}
	return *p
}

func nullF64(p *float64) driver.Value {
	if p == nil {
		return nil
	}
	return *p
}

// MemoryUsage reports duckdb's own accounting, which the go runtime cannot see
// at all: every byte of it reaches rss as cgo memory. memwatch calls this when
// the process crosses its threshold, so an empty string on a closed or broken
// handle is the right answer rather than an error.
func (h *History) MemoryUsage() string {
	rows, err := h.db.Query(
		"SELECT tag, memory_usage_bytes FROM duckdb_memory()" +
			" WHERE memory_usage_bytes > 0 ORDER BY memory_usage_bytes DESC LIMIT 5")
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	var total int64
	for rows.Next() {
		var tag string
		var b int64
		if rows.Scan(&tag, &b) != nil {
			return ""
		}
		total += b
		parts = append(parts, fmt.Sprintf("%s %.0fmb", tag, float64(b)/1e6))
	}
	if len(parts) == 0 {
		return "duckdb: nothing pinned"
	}
	return fmt.Sprintf("duckdb: %.0fmb pinned (%s)", float64(total)/1e6, strings.Join(parts, ", "))
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
