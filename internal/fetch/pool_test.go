package fetch

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
)

// gauge tracks how many workers were inside a flow at once, which is how the
// pool widths are observed from the outside.
type gauge struct {
	mu      sync.Mutex
	cur, hi int
}

func (g *gauge) enter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cur++
	if g.cur > g.hi {
		g.hi = g.cur
	}
}

func (g *gauge) exit() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cur--
}

func (g *gauge) peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hi
}

type logger struct {
	mu    sync.Mutex
	lines []string
}

func (l *logger) f(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logger) grep(sub string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			out = append(out, s)
		}
	}
	return out
}

// calls records the order flows were entered in.
type calls struct {
	mu   sync.Mutex
	seen []string
}

func (c *calls) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, s)
}

func (c *calls) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.seen, ",")
}

// poolSrc builds a source that dispatches to a flow of the same name. the url
// only decides which of the two pools it lands in.
func poolSrc(t *testing.T, name, url string) *config.Source {
	t.Helper()
	return mustSource(t, `{"name":%q,"flow":%q,"url":%q}`, name, name, url)
}

const ghURL = "https://raw.githubusercontent.com/a/b/main/list.txt"

// one returns a flow yielding a single record, so a source's contribution is
// identifiable in the merged output.
func one(ip string) []*extract.Record {
	return []*extract.Record{{IP: ip, Port: 8080}}
}

func TestIsGithub(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{ghURL, true},
		{"http://raw.githubusercontent.com/x", true},
		// python compares netloc, which keeps the port, so this is a different host
		{"https://raw.githubusercontent.com:443/x", false},
		{"https://github.com/x", false},
		{"https://gist.raw.githubusercontent.com/x", false},
		{"", false},
		{"://nope", false},
	}
	for _, c := range cases {
		var src config.Source
		src.URL = c.url
		if got := IsGithub(&src); got != c.want {
			t.Errorf("IsGithub(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestDeriveWorkers(t *testing.T) {
	ceiling := max(16, runtime.NumCPU()*4)
	cases := [][2]int{{0, 1}, {1, 1}, {5, 5}, {1000, ceiling}}
	for _, c := range cases {
		if got := deriveWorkers(c[0]); got != c[1] {
			t.Errorf("deriveWorkers(%d) = %d, want %d", c[0], got, c[1])
		}
	}
	if ceiling < 16 {
		t.Fatalf("ceiling = %d, want at least 16", ceiling)
	}
}

// TestTwoPoolsRunAtTheirOwnWidths proves the split: github sources are capped
// at three no matter how wide the main pool is, and the two pools run at once.
func TestTwoPoolsRunAtTheirOwnWidths(t *testing.T) {
	var gh, rest, all gauge
	flows := map[string]FlowFunc{}
	var sources []*config.Source
	for i := 0; i < 6; i++ {
		g, name, url := &rest, fmt.Sprintf("plain%d", i), "https://example.com/"
		if i%2 == 0 {
			g, name, url = &gh, fmt.Sprintf("gh%d", i), ghURL
		}
		flows[name] = func(context.Context, *Flow) ([]*extract.Record, []string) {
			g.enter()
			all.enter()
			defer g.exit()
			defer all.exit()
			time.Sleep(60 * time.Millisecond)
			return nil, nil
		}
		sources = append(sources, poolSrc(t, name, url))
	}

	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 2}
	if _, errs, stats := p.Run(context.Background(), sources); len(errs) != 0 || len(stats) != 6 {
		t.Fatalf("errs=%v stats=%d", errs, len(stats))
	}
	if got := gh.peak(); got != 3 {
		t.Errorf("github pool peak = %d, want 3", got)
	}
	if got := rest.peak(); got != 2 {
		t.Errorf("main pool peak = %d, want 2", got)
	}
	// both widths at once, which is what proves the pools overlap rather than
	// taking turns
	if got := all.peak(); got != 5 {
		t.Errorf("combined peak = %d, want 5", got)
	}
}

func TestRecordsComeBackInSourcesOrder(t *testing.T) {
	// c finishes first and a last, so completion order is the reverse of the
	// declared order
	delays := map[string]time.Duration{"a": 60 * time.Millisecond, "b": 30 * time.Millisecond, "c": 0}
	ipOf := map[string]string{"a": "1.2.3.1", "b": "1.2.3.2", "c": "1.2.3.3"}
	flows := map[string]FlowFunc{}
	var sources []*config.Source
	for _, name := range []string{"a", "b", "c"} {
		n := name
		flows[n] = func(context.Context, *Flow) ([]*extract.Record, []string) {
			time.Sleep(delays[n])
			return one(ipOf[n]), []string{n + ": noted"}
		}
		sources = append(sources, poolSrc(t, n, "https://example.com/"+n))
	}

	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 3}
	recs, errs, stats := p.Run(context.Background(), sources)
	if got := strings.Join(ips(recs), " "); got != "1.2.3.1:8080 1.2.3.2:8080 1.2.3.3:8080" {
		t.Fatalf("records = %s", got)
	}
	// errors are appended as sources finish, so they are in completion order
	if got := strings.Join(errs, ","); got != "c: noted,b: noted,a: noted" {
		t.Fatalf("errors = %s", got)
	}
	if st := stats["a"]; st.Fetched != 1 || st.Requeues != 0 || st.Elapsed < 60*time.Millisecond {
		t.Fatalf("stats[a] = %+v", st)
	}
}

func TestRequeueExhaustsAttempts(t *testing.T) {
	defer func(d time.Duration) { RequeueCooldown = d }(RequeueCooldown)
	RequeueCooldown = 60 * time.Millisecond

	var n int
	var mu sync.Mutex
	flows := map[string]FlowFunc{"rl": func(context.Context, *Flow) ([]*extract.Record, []string) {
		mu.Lock()
		defer mu.Unlock()
		n++
		return nil, []string{"rl: fetch failed: 429 Client Error: Too Many Requests for url: x"}
	}}
	var lg logger
	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 4, Logf: lg.f}

	recs, errs, stats := p.Run(context.Background(), []*config.Source{
		poolSrc(t, "rl", "https://example.com/"),
	})
	// the first attempt plus MaxRequeues more
	if n != MaxRequeues+1 {
		t.Fatalf("attempts = %d, want %d", n, MaxRequeues+1)
	}
	// only the final attempt's error is kept; the requeued ones are dropped
	if len(recs) != 0 || len(errs) != 1 {
		t.Fatalf("recs=%v errs=%v", ips(recs), errs)
	}
	if st := stats["rl"]; st.Requeues != MaxRequeues {
		t.Fatalf("stats = %+v", st)
	}
	want := []string{
		"rl                       429 -> requeued (1/2)",
		"rl                       429 -> requeued (2/2)",
	}
	got := lg.grep("requeued")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log =\n%q\nwant\n%q", got, want)
	}
}

// TestRequeueReleasesItsSlot is the reason the pool is not an errgroup: a
// cooling-down source must not hold a worker.
func TestRequeueReleasesItsSlot(t *testing.T) {
	defer func(d time.Duration) { RequeueCooldown = d }(RequeueCooldown)
	RequeueCooldown = 120 * time.Millisecond

	var c calls
	flows := map[string]FlowFunc{
		"rl": func(context.Context, *Flow) ([]*extract.Record, []string) {
			c.add("rl")
			return nil, []string{"rl: 429"}
		},
		"ok": func(context.Context, *Flow) ([]*extract.Record, []string) {
			c.add("ok")
			return one("1.2.3.4"), nil
		},
	}
	// one worker, so ok can only run if rl gave the slot back
	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 1}
	recs, _, _ := p.Run(context.Background(), []*config.Source{
		poolSrc(t, "rl", "https://example.com/a"),
		poolSrc(t, "ok", "https://example.com/b"),
	})
	if got := c.joined(); got != "rl,ok,rl,rl" {
		t.Fatalf("calls = %s, want rl,ok,rl,rl", got)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %v", ips(recs))
	}
}

func TestNonRateLimitErrorIsNotRequeued(t *testing.T) {
	var n int
	flows := map[string]FlowFunc{"e": func(context.Context, *Flow) ([]*extract.Record, []string) {
		n++
		return nil, []string{"e: fetch failed: 404 Client Error"}
	}}
	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 2}
	_, errs, _ := p.Run(context.Background(), []*config.Source{poolSrc(t, "e", "https://x/")})
	if n != 1 || len(errs) != 1 {
		t.Fatalf("attempts=%d errs=%v", n, errs)
	}
}

func TestPanickingFlowIsContained(t *testing.T) {
	flows := map[string]FlowFunc{
		"boom": func(context.Context, *Flow) ([]*extract.Record, []string) {
			panic("kaboom")
		},
		"fine": func(context.Context, *Flow) ([]*extract.Record, []string) {
			return one("1.2.3.4"), nil
		},
	}
	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 2}
	recs, errs, stats := p.Run(context.Background(), []*config.Source{
		poolSrc(t, "boom", "https://x/a"),
		poolSrc(t, "fine", "https://x/b"),
	})
	if len(recs) != 1 || recs[0].IP != "1.2.3.4" {
		t.Fatalf("records = %v", ips(recs))
	}
	if len(errs) != 1 || errs[0] != "boom: crashed: kaboom" {
		t.Fatalf("errors = %v", errs)
	}
	if st := stats["boom"]; st.Fetched != 0 {
		t.Fatalf("stats[boom] = %+v", st)
	}
}

func TestWatchdogNamesSlowSources(t *testing.T) {
	defer func(a, b time.Duration) { slowAfter, watchTick = a, b }(slowAfter, watchTick)
	slowAfter, watchTick = 40*time.Millisecond, 10*time.Millisecond

	flows := map[string]FlowFunc{
		"slowpoke": func(_ context.Context, fl *Flow) ([]*extract.Record, []string) {
			fl.State.Request("http://x/")
			time.Sleep(200 * time.Millisecond)
			return nil, nil
		},
		"quick": func(context.Context, *Flow) ([]*extract.Record, []string) {
			return nil, nil
		},
	}
	var lg logger
	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 2, Logf: lg.f}
	p.Run(context.Background(), []*config.Source{
		poolSrc(t, "slowpoke", "https://x/a"),
		poolSrc(t, "quick", "https://x/b"),
	})

	slow := lg.grep("[slow ")
	if len(slow) == 0 {
		t.Fatal("the watchdog never named the slow source")
	}
	if !strings.Contains(slow[0], "] slowpoke: reqs=1 page=- url=http://x/") {
		t.Fatalf("line = %q", slow[0])
	}
	// 200ms at a 40ms window is at most 5 announcements, and a source that
	// finished immediately is never named
	if len(slow) > 5 {
		t.Errorf("announced %d times: %v", len(slow), slow)
	}
	if got := lg.grep("] quick:"); len(got) != 0 {
		t.Errorf("named a fast source: %v", got)
	}
}

// TestCancelDuringCooldownDoesNotWaitItOut covers the escape from a cooldown
// that would otherwise outlive the run.
func TestCancelDuringCooldownDoesNotWaitItOut(t *testing.T) {
	defer func(d time.Duration) { RequeueCooldown = d }(RequeueCooldown)
	RequeueCooldown = 30 * time.Second

	flows := map[string]FlowFunc{"rl": func(ctx context.Context, _ *Flow) ([]*extract.Record, []string) {
		if ctx.Err() != nil {
			return nil, []string{"rl: dead"}
		}
		return nil, []string{"rl: 429"}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()

	p := &Pool{F: &Fetcher{Flows: flows}, Workers: 2}
	start := time.Now()
	_, errs, stats := p.Run(ctx, []*config.Source{poolSrc(t, "rl", "https://x/")})
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("waited %s for a cancelled run", d)
	}
	if len(errs) != 1 || errs[0] != "rl: dead" {
		t.Fatalf("errors = %v", errs)
	}
	// the requeue still happened, so the count reflects it
	if st := stats["rl"]; st.Requeues != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestHasRateLimit(t *testing.T) {
	if hasRateLimit(nil) || hasRateLimit([]string{"500 Server Error"}) {
		t.Error("false positive")
	}
	if !hasRateLimit([]string{"a", "429 Client Error"}) {
		t.Error("missed a 429")
	}
	// a substring test, so a url carrying the digits counts too. python does the
	// same and the cost is one needless requeue.
	if !hasRateLimit([]string{"t: fetch failed: 404 for url: http://x/p429.txt"}) {
		t.Error("substring behavior changed")
	}
}

func TestDash(t *testing.T) {
	if dash(0) != "-" || dash(7) != "7" {
		t.Error("dash")
	}
	if dashStr("") != "-" || dashStr("u") != "u" {
		t.Error("dashStr")
	}
}

func TestEmptyRunIsClean(t *testing.T) {
	p := &Pool{F: &Fetcher{}}
	recs, errs, stats := p.Run(context.Background(), nil)
	if len(recs) != 0 || len(errs) != 0 || len(stats) != 0 {
		t.Fatalf("recs=%v errs=%v stats=%v", recs, errs, stats)
	}
}
