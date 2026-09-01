package history

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/check"
	"github.com/M1noa/proxypool/internal/extract"
)

func strp(s string) *string { return &s }
func fp(f float64) *float64 { return &f }
func ip(n int) *int         { return &n }

func openTest(t *testing.T) *History {
	t.Helper()
	h, err := Open(filepath.Join(t.TempDir(), "history.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func TestOpenFreshDBIsEmpty(t *testing.T) {
	h := openTest(t)
	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("StateMap on a fresh db = %v, want empty", state)
	}
}

// the workflow's restore step leaves a zero-byte file when the download 404s,
// which is exactly what the first real run sees.
func TestOpenReplacesZeroByteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.duckdb")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a zero-byte file: %v", err)
	}
	defer h.Close()
	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("StateMap = %v, want empty", state)
	}
}

func TestOpenRebuildsOnSchemaMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.duckdb")
	h, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := h.UpdateState([]check.Outcome{{IP: "1.2.3.4", Port: 8080, Proto: "http", Alive: true, RT: ip(100)}},
		"2026-01-01T00:00:00Z", map[Key]State{}); err != nil {
		t.Fatalf("seed UpdateState: %v", err)
	}
	if _, err := h.db.Exec(
		"UPDATE meta SET v = '2' WHERE k = 'schema_version'"); err != nil {
		t.Fatalf("force old schema version: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	h2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h2.Close()
	state, err := h2.StateMap()
	if err != nil {
		t.Fatalf("StateMap after reopen: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("StateMap after a schema-version mismatch = %v, want empty (rebuilt table)", state)
	}
}

func TestUpdateStateBrandNewProxy(t *testing.T) {
	h := openTest(t)
	ts := "2026-01-01T00:00:00Z"
	outcomes := []check.Outcome{{IP: "1.2.3.4", Port: 8080, Proto: "http", Alive: true, RT: ip(200)}}
	if err := h.UpdateState(outcomes, ts, map[Key]State{}); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	st, ok := state[Key{"1.2.3.4", 8080, "http"}]
	if !ok {
		t.Fatal("StateMap missing the row just written")
	}
	if st.FailsStreak != 0 || st.OKCount != 1 || st.CheckCount != 1 {
		t.Errorf("state = %+v, want streak=0 ok=1 checks=1", st)
	}
	if st.LastOKTS == nil || *st.LastOKTS != ts {
		t.Errorf("LastOKTS = %v, want %q", st.LastOKTS, ts)
	}
	if st.FirstSeenTS == nil || *st.FirstSeenTS != ts {
		t.Errorf("FirstSeenTS = %v, want %q (first update seeds it)", st.FirstSeenTS, ts)
	}
	if st.RelEMA == nil || *st.RelEMA != 1.0 {
		t.Errorf("RelEMA = %v, want 1.0 (float(alive) when prior is nil)", st.RelEMA)
	}
	if st.RTEMA == nil || *st.RTEMA != 200.0 {
		t.Errorf("RTEMA = %v, want 200.0", st.RTEMA)
	}
}

func TestUpdateStateFailureKeepsRTEMAAndFirstSeen(t *testing.T) {
	h := openTest(t)
	prev := map[Key]State{
		{"1.2.3.4", 8080, "http"}: {
			FailsStreak: 2, LastOKTS: strp("2025-01-01T00:00:00Z"),
			FirstSeenTS: strp("2024-01-01T00:00:00Z"),
			RelEMA:      fp(0.5), RTEMA: fp(1000),
			OKCount: 3, CheckCount: 5,
		},
	}
	ts := "2026-01-01T00:00:00Z"
	outcomes := []check.Outcome{{IP: "1.2.3.4", Port: 8080, Proto: "http", Alive: false}}
	if err := h.UpdateState(outcomes, ts, prev); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	st := state[Key{"1.2.3.4", 8080, "http"}]
	if st.FailsStreak != 3 {
		t.Errorf("FailsStreak = %d, want 3", st.FailsStreak)
	}
	if st.LastOKTS == nil || *st.LastOKTS != "2025-01-01T00:00:00Z" {
		t.Errorf("LastOKTS = %v, want the untouched prior value (this probe failed)", st.LastOKTS)
	}
	if st.FirstSeenTS == nil || *st.FirstSeenTS != "2024-01-01T00:00:00Z" {
		t.Errorf("FirstSeenTS = %v, want the untouched prior value", st.FirstSeenTS)
	}
	// 6th check, so warmAlpha is 1/6 rather than the fixed emaAlpha
	wantRel := 0.5 + (1.0/6.0)*(0-0.5)
	if st.RelEMA == nil || math.Abs(*st.RelEMA-wantRel) > 1e-12 {
		t.Errorf("RelEMA = %v, want %v", st.RelEMA, wantRel)
	}
	if st.RTEMA == nil || *st.RTEMA != 1000 {
		t.Errorf("RTEMA = %v, want 1000 (untouched: a failed probe carries no rt)", st.RTEMA)
	}
	if st.OKCount != 3 || st.CheckCount != 6 {
		t.Errorf("counts = ok:%d check:%d, want ok:3 check:6", st.OKCount, st.CheckCount)
	}
}

// a young proxy's rel_ema has to track its actual record. under the fixed
// emaAlpha the first outcome pinned it: one failure then six successes left it at
// 0.069, because each later check only closed 1.18% of the gap toward 1. the db
// holds a median of 5 checks per proxy, so essentially every row was pinned.
func TestNextStateRelEMAWarmsToRunningMean(t *testing.T) {
	ts := "2026-01-01T00:00:00Z"
	fold := func(alive ...bool) State {
		var st State
		for _, a := range alive {
			st = nextState(check.Outcome{IP: "1.2.3.4", Port: 80, Proto: "http", Alive: a}, ts, st)
		}
		return st
	}

	// one failure then six successes: 6 of 7 checks ok, and below 85 checks the
	// ema is exactly that mean rather than a lagging fraction of it.
	st := fold(false, true, true, true, true, true, true)
	if st.CheckCount != 7 || st.OKCount != 6 {
		t.Fatalf("counts = ok:%d check:%d, want ok:6 check:7", st.OKCount, st.CheckCount)
	}
	if st.RelEMA == nil || math.Abs(*st.RelEMA-6.0/7.0) > 1e-12 {
		t.Errorf("RelEMA = %v, want %v (ok_count/check_count)", st.RelEMA, 6.0/7.0)
	}

	// all-dead and all-alive stay at the ends
	if st := fold(false, false, false); st.RelEMA == nil || *st.RelEMA != 0 {
		t.Errorf("RelEMA after 3 failures = %v, want 0", st.RelEMA)
	}
	if st := fold(true, true, true); st.RelEMA == nil || *st.RelEMA != 1 {
		t.Errorf("RelEMA after 3 successes = %v, want 1", st.RelEMA)
	}

	// past the crossover the fixed window takes over, so recent outcomes start
	// mattering more than lifetime ones — the behavior emaAlpha was chosen for.
	if got := warmAlpha(85); got != emaAlpha {
		t.Errorf("warmAlpha(85) = %v, want emaAlpha %v", got, emaAlpha)
	}
	if got := warmAlpha(84); got != 1.0/84.0 {
		t.Errorf("warmAlpha(84) = %v, want 1/84 %v", got, 1.0/84.0)
	}
}

// outcomes fold in through a set-based delete-then-insert, so a batch mixing a
// key already in proxy_state with a brand new one exercises both halves at once:
// the delete has to find the first and tolerate the second missing. the
// single-row loop this replaced could not get one branch wrong without getting
// both wrong, and this can.
func TestUpdateStateBatchMixesInsertAndUpdate(t *testing.T) {
	h := openTest(t)
	ts1, ts2 := "2026-01-01T00:00:00Z", "2026-01-01T01:00:00Z"

	seed := check.Outcome{IP: "1.1.1.1", Port: 80, Proto: "http", Alive: true, RT: ip(100)}
	if err := h.UpdateState([]check.Outcome{seed}, ts1, map[Key]State{}); err != nil {
		t.Fatalf("seed UpdateState: %v", err)
	}
	prev, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}

	if err := h.UpdateState([]check.Outcome{
		{IP: "1.1.1.1", Port: 80, Proto: "http", Alive: false},              // conflicts
		{IP: "2.2.2.2", Port: 81, Proto: "socks5", Alive: true, RT: ip(50)}, // fresh
	}, ts2, prev); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if len(state) != 2 {
		t.Fatalf("proxy_state holds %d rows, want 2", len(state))
	}
	if st := state[Key{"1.1.1.1", 80, "http"}]; st.CheckCount != 2 || st.FailsStreak != 1 || st.OKCount != 1 {
		t.Errorf("conflicting row = %+v, want checks=2 streak=1 ok=1", st)
	}
	if st := state[Key{"2.2.2.2", 81, "socks5"}]; st.CheckCount != 1 || st.OKCount != 1 {
		t.Errorf("fresh row = %+v, want checks=1 ok=1", st)
	}
}

// a batch is only as safe as merge()'s dedupe upstream of it, and without
// insertSQL's QUALIFY a repeated key would insert twice and quietly double the
// row. collapsing to the last outcome keeps it behaving the way the
// row-at-a-time loop did.
func TestUpdateStateDuplicateKeyKeepsLastOutcome(t *testing.T) {
	h := openTest(t)
	ts := "2026-01-01T00:00:00Z"
	if err := h.UpdateState([]check.Outcome{
		{IP: "1.1.1.1", Port: 80, Proto: "http", Alive: true, RT: ip(100)},
		{IP: "1.1.1.1", Port: 80, Proto: "http", Alive: false},
	}, ts, map[Key]State{}); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if len(state) != 1 {
		t.Fatalf("proxy_state holds %d rows, want 1", len(state))
	}
	st := state[Key{"1.1.1.1", 80, "http"}]
	if st.FailsStreak != 1 || st.LastOKTS != nil {
		t.Errorf("state = %+v, want the later failing outcome (streak=1, no last_ok_ts)", st)
	}
}

// a real run stages ~2m outcomes in stageBatch-sized chunks, so every flush but
// the last runs against a proxy_state the same call already wrote to. the risk
// is deleteSQL: it has to drop only what the batch in staging supersedes, and a
// batch boundary must not cost the earlier batches their rows.
func TestUpdateStateAcrossBatches(t *testing.T) {
	defer func(n int) { stageBatch = n }(stageBatch)
	stageBatch = 3

	h := openTest(t)
	if err := h.UpdateState([]check.Outcome{
		{IP: "9.9.9.9", Port: 80, Proto: "http", Alive: true, RT: ip(100)},
	}, "2026-01-01T00:00:00Z", map[Key]State{}); err != nil {
		t.Fatalf("seed UpdateState: %v", err)
	}
	prev, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}

	// 7 outcomes over 3 batches. the seeded key sits in the last one, so its
	// update happens after two flushes have already written to proxy_state.
	outcomes := make([]check.Outcome, 0, 7)
	for i := 0; i < 6; i++ {
		outcomes = append(outcomes, check.Outcome{
			IP: "10.0.0." + strconv.Itoa(i), Port: 8080, Proto: "http", Alive: true, RT: ip(50)})
	}
	outcomes = append(outcomes, check.Outcome{IP: "9.9.9.9", Port: 80, Proto: "http", Alive: false})

	if err := h.UpdateState(outcomes, "2026-01-02T00:00:00Z", prev); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if len(state) != 7 {
		t.Fatalf("proxy_state holds %d rows, want 7 — a batch dropped an earlier batch's rows", len(state))
	}
	for i := 0; i < 6; i++ {
		k := Key{"10.0.0." + strconv.Itoa(i), 8080, "http"}
		if st, ok := state[k]; !ok || st.CheckCount != 1 {
			t.Errorf("state[%v] = %+v (present=%v), want one check", k, st, ok)
		}
	}
	// the seeded row was superseded, not duplicated, across the boundary
	if st := state[Key{"9.9.9.9", 80, "http"}]; st.CheckCount != 2 || st.FailsStreak != 1 || st.OKCount != 1 {
		t.Errorf("seeded state = %+v, want checks=2 streak=1 ok=1", st)
	}
}

func TestScores(t *testing.T) {
	h := openTest(t)
	// insert directly so the values under test aren't also exercising
	// UpdateState's own ema arithmetic.
	if _, err := h.db.Exec(
		`INSERT INTO proxy_state (ip, port, proto, fails_streak, last_ok_ts,
			last_checked_ts, first_seen_ts, rel_ema, rt_ema, ok_count, check_count)
		 VALUES ('1.2.3.4', 8080, 'http', 0, 't', 't', 'first', 0.9, 500, 9, 10)`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	// a brand new proxy: no rt_ema yet, so speed falls back to NewProxyScore.
	if _, err := h.db.Exec(
		`INSERT INTO proxy_state (ip, port, proto, fails_streak, last_ok_ts,
			last_checked_ts, first_seen_ts, rel_ema, rt_ema, ok_count, check_count)
		 VALUES ('5.6.7.8', 8081, 'https', 0, NULL, 't2', 'first2', NULL, NULL, 1, 1)`); err != nil {
		t.Fatalf("seed row 2: %v", err)
	}

	scores, err := h.Scores()
	if err != nil {
		t.Fatalf("Scores: %v", err)
	}

	// rel_ema=0.9, ok=9/10 -> rel = 0.7*0.9 + 0.3*0.9 = 0.9; rt_ema==rtGoodMS
	// -> speed = 1.0 exactly.
	s1, ok := scores[Key{"1.2.3.4", 8080, "http"}]
	if !ok {
		t.Fatal("missing scored row 1")
	}
	if s1.Reliability != 0.9 {
		t.Errorf("row1 Reliability = %v, want 0.9", s1.Reliability)
	}
	if s1.Quality != 0.9 {
		t.Errorf("row1 Quality = %v, want 0.9", s1.Quality)
	}
	if s1.ChecksTotal != 10 || s1.ChecksOK != 9 {
		t.Errorf("row1 counts = %d/%d, want 10/9", s1.ChecksTotal, s1.ChecksOK)
	}

	// rel_ema NULL -> relEMAv 0, relAll=1/1=1.0 -> rel = 0.3*1.0 = 0.3;
	// rt_ema NULL -> speed falls back to NewProxyScore (0.5).
	s2, ok := scores[Key{"5.6.7.8", 8081, "https"}]
	if !ok {
		t.Fatal("missing scored row 2")
	}
	if s2.Reliability != 0.3 {
		t.Errorf("row2 Reliability = %v, want 0.3", s2.Reliability)
	}
	if s2.Quality != 0.15 {
		t.Errorf("row2 Quality = %v, want 0.15", s2.Quality)
	}
}

func TestPruneDropsOldRows(t *testing.T) {
	h := openTest(t)
	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -pruneDays-1).Format(tsFmt)
	recent := now.AddDate(0, 0, -1).Format(tsFmt)

	if _, err := h.db.Exec(
		`INSERT INTO proxy_state (ip, port, proto, last_checked_ts) VALUES ('1.2.3.4', 1, 'http', ?)`, old); err != nil {
		t.Fatalf("seed old row: %v", err)
	}
	if _, err := h.db.Exec(
		`INSERT INTO proxy_state (ip, port, proto, last_checked_ts) VALUES ('5.6.7.8', 2, 'http', ?)`, recent); err != nil {
		t.Fatalf("seed recent row: %v", err)
	}

	n, err := h.Prune(now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune() = %d, want 1", n)
	}

	state, err := h.StateMap()
	if err != nil {
		t.Fatalf("StateMap: %v", err)
	}
	if _, ok := state[Key{"1.2.3.4", 1, "http"}]; ok {
		t.Error("old row survived Prune")
	}
	if _, ok := state[Key{"5.6.7.8", 2, "http"}]; !ok {
		t.Error("recent row was pruned, want it kept")
	}
}

// fakeSource is a rand.Source returning a fixed value from Int63, so
// rand.Rand.Float64() is deterministic across a test.
type fakeSource struct{ v int64 }

func (f fakeSource) Int63() int64 { return f.v }
func (f fakeSource) Seed(int64)   {}

func rngAlwaysZero() *rand.Rand { return rand.New(fakeSource{0}) }

// rngAlwaysNearOne returns a rand.Rand whose Float64() is always ~0.95: well
// above maxSkipProb (0.9), so no p ever clears it. math.MaxInt64 itself
// isn't usable here — float64(math.MaxInt64) rounds up to exactly 1<<63, so
// Float64() computes exactly 1.0 and rand's own "never return 1.0" guard
// spins forever re-drawing the same constant value.
func rngAlwaysNearOne() *rand.Rand {
	f := 0.95 * 9223372036854775808.0 // runtime float, not a constant expression
	return rand.New(fakeSource{int64(f)})
}

func TestSkipKeysSkipsOnLowRoll(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-1 * time.Hour).Format(tsFmt)
	records := []*extract.Record{{IP: "1.2.3.4", Port: 8080, Protocols: []string{"http"}}}
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}: {FailsStreak: 5, LastCheckedTS: strp(last)},
	}

	skips := SkipKeys(records, state, now, rngAlwaysZero())
	if !skips[check.SkipKey{IP: "1.2.3.4", Port: 8080, Proto: "http"}] {
		t.Error("SkipKeys with a zero roll = not skipped, want skipped")
	}
}

func TestSkipKeysNeverSkipsOnHighRoll(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-1 * time.Hour).Format(tsFmt)
	records := []*extract.Record{{IP: "1.2.3.4", Port: 8080, Protocols: []string{"http"}}}
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}: {FailsStreak: 23, LastCheckedTS: strp(last)},
	}

	skips := SkipKeys(records, state, now, rngAlwaysNearOne())
	if len(skips) != 0 {
		t.Errorf("SkipKeys with a near-1 roll = %v, want none (max skip prob is 0.9)", skips)
	}
}

func TestSkipKeysNoStreakNeverSkips(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	records := []*extract.Record{{IP: "1.2.3.4", Port: 8080, Protocols: []string{"http"}}}
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}: {FailsStreak: 0},
	}
	if skips := SkipKeys(records, state, now, rngAlwaysZero()); len(skips) != 0 {
		t.Errorf("SkipKeys with fails_streak=0 = %v, want none", skips)
	}
}

func TestSkipKeysRecheckFloorOverridesStreak(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tooOld := now.Add(-25 * time.Hour).Format(tsFmt) // past recheckEveryHours
	records := []*extract.Record{{IP: "1.2.3.4", Port: 8080, Protocols: []string{"http"}}}
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}: {FailsStreak: 20, LastCheckedTS: strp(tooOld)},
	}
	if skips := SkipKeys(records, state, now, rngAlwaysZero()); len(skips) != 0 {
		t.Errorf("SkipKeys past the recheck floor = %v, want none even with a zero roll", skips)
	}
}

func TestSkipKeysDefaultsMissingProtocols(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	last := now.Add(-1 * time.Hour).Format(tsFmt)
	// no Protocols set: claimed must default to ["http", "https"].
	records := []*extract.Record{{IP: "1.2.3.4", Port: 8080}}
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}:  {FailsStreak: 5, LastCheckedTS: strp(last)},
		{"1.2.3.4", 8080, "https"}: {FailsStreak: 5, LastCheckedTS: strp(last)},
	}
	skips := SkipKeys(records, state, now, rngAlwaysZero())
	if len(skips) != 2 {
		t.Errorf("SkipKeys with unclaimed protocols = %v, want both http and https considered", skips)
	}
}

func TestAliveMap(t *testing.T) {
	state := map[Key]State{
		{"1.2.3.4", 8080, "http"}:  {LastOKTS: strp("t")},
		{"1.2.3.4", 8080, "https"}: {}, // no last_ok_ts on this proto
		{"5.6.7.8", 8081, "http"}:  {},
	}
	got := AliveMap(state)
	if !got[check.AliveKey{IP: "1.2.3.4", Port: 8080}] {
		t.Error("AliveMap missing 1.2.3.4:8080, which has a proto with last_ok_ts")
	}
	if got[check.AliveKey{IP: "5.6.7.8", Port: 8081}] {
		t.Error("AliveMap includes 5.6.7.8:8081, which has never gone alive")
	}
}

func TestNowFormat(t *testing.T) {
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	if got := Now(); !re.MatchString(got) {
		t.Errorf("Now() = %q, want to match %s", got, re.String())
	}
}
