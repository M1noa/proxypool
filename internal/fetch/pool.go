package fetch

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
)

// requeue policy and the small pool raw.githubusercontent.com gets, verbatim
// from fetch_proxies.py.
const (
	MaxRequeues   = 2
	GithubWorkers = 3
)

// the timings are vars only so the tests do not have to wait out a real
// cooldown or a real 8s watchdog window.
var (
	// RequeueCooldown is how long a rate limited source sits out.
	RequeueCooldown = 20 * time.Second
	// slowAfter is how long a source runs before the watchdog starts naming it,
	// and also how often it is named again. watchTick is how often it looks.
	slowAfter = 8 * time.Second
	watchTick = time.Second
)

// Stat is one source's line in the run summary. the readme's per-source table
// reads Fetched; the rest is stdout only.
type Stat struct {
	Fetched  int
	Requests int
	Elapsed  time.Duration
	Requeues int
}

// Pool runs every source across two worker pools: a wide one, and a three-wide
// one for raw.githubusercontent.com, which rate limits.
type Pool struct {
	F *Fetcher
	// Workers overrides the derived width of the main pool. zero derives it.
	Workers int
	// Logf takes the per-source, requeue and watchdog lines. nil discards them.
	Logf func(format string, args ...any)
}

// IsGithub decides which pool a source belongs to.
//
// python compares urlparse().netloc, which carries the port and any userinfo,
// so Host is the match and Hostname would not be. no source has userinfo, so
// the one difference between them does not arise.
func IsGithub(src *config.Source) bool {
	u, err := url.Parse(src.URL)
	if err != nil {
		return false
	}
	return u.Host == "raw.githubusercontent.com"
}

// deriveWorkers is python's `min(len(rest) or 1, max(16, (os.cpu_count() or 4)
// * 4))`: never wider than the work there is, never narrower than 16.
func deriveWorkers(n int) int {
	return min(max(n, 1), max(16, runtime.NumCPU()*4))
}

// Run fetches every source and returns its records, every error reported along
// the way, and per-source stats.
//
// records come back in sources order rather than completion order, which is
// what keeps a run's output stable.
func (p *Pool) Run(ctx context.Context, sources []*config.Source) ([]*extract.Record, []string, map[string]Stat) {
	var gh, rest []*config.Source
	for _, s := range sources {
		if IsGithub(s) {
			gh = append(gh, s)
		} else {
			rest = append(rest, s)
		}
	}
	workers := p.Workers
	if workers <= 0 {
		workers = deriveWorkers(len(rest))
	}

	sch := &sched{
		p:      p,
		states: map[string]*State{},
		res:    map[string]*outcome{},
	}

	stop := make(chan struct{})
	var wd sync.WaitGroup
	wd.Add(1)
	go func() {
		defer wd.Done()
		sch.watch(stop)
	}()

	var pools sync.WaitGroup
	pools.Add(2)
	go func() {
		defer pools.Done()
		sch.pool(ctx, rest, workers)
	}()
	go func() {
		defer pools.Done()
		sch.pool(ctx, gh, GithubWorkers)
	}()
	pools.Wait()
	close(stop)
	wd.Wait()

	// sized up front: a full run is a couple of million records, and growing
	// there from nil copies tens of megabytes and holds 2x at the last regrow
	total := 0
	for _, o := range sch.res {
		total += len(o.recs)
	}
	recs := make([]*extract.Record, 0, total)
	stats := make(map[string]Stat, len(sch.res))
	for _, s := range sources {
		o := sch.res[s.Name]
		if o == nil {
			// nothing recorded means the source never finished, which only
			// happens if a pool was cut short
			continue
		}
		recs = append(recs, o.recs...)
		stats[s.Name] = Stat{
			Fetched: len(o.recs), Requests: o.requests,
			Elapsed: o.elapsed, Requeues: o.requeues,
		}
	}
	return recs, sch.errs, stats
}

// outcome is what one finished source contributed.
type outcome struct {
	recs     []*extract.Record
	requests int
	elapsed  time.Duration
	requeues int
}

// sched is the mutable half of a Run: both pools and the watchdog share it.
// python shares plain dicts and relies on the gil; the reads here are real
// concurrent reads, so they take the mutex.
type sched struct {
	p *Pool

	mu     sync.Mutex
	states map[string]*State
	// order is the sequence names were first dispatched in, so the watchdog
	// reports them in the order python's dict iteration would.
	order []string
	res   map[string]*outcome
	errs  []string
}

func (s *sched) logf(format string, args ...any) {
	if s.p.Logf != nil {
		s.p.Logf(format, args...)
	}
}

// state hands out one State per source name, reused across requeues so a
// requeued source keeps reporting under the same entry.
func (s *sched) state(name string) *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.states[name]; st != nil {
		return st
	}
	st := &State{}
	s.states[name] = st
	s.order = append(s.order, name)
	return st
}

func (s *sched) finish(name string, o *outcome, errs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res[name] = o
	s.errs = append(s.errs, errs...)
}

// job is a queue entry: a source, how many times it has already been requeued,
// and the earliest it may be dispatched.
type job struct {
	src       *config.Source
	attempts  int
	notBefore time.Time
}

type done struct {
	job     job
	st      *State
	elapsed time.Duration
	recs    []*extract.Record
	errs    []string
}

// pool is python's run_pool: a scheduler that dispatches from the head of a
// queue and takes completions as they arrive.
//
// the width cannot be an errgroup limit, because a source that answers 429
// goes back on the queue and has to give its slot up for the whole cooldown.
func (s *sched) pool(ctx context.Context, sources []*config.Source, workers int) {
	pending := make([]job, len(sources))
	for i, src := range sources {
		pending[i] = job{src: src}
	}
	ch := make(chan done, len(sources)+1)
	inflight := 0

	for len(pending) > 0 || inflight > 0 {
		// dispatch from the head only. a cooling-down source at the front holds
		// everything behind it, which is python's behavior and is mostly benign:
		// requeues are appended in completion order, so their cooldowns expire in
		// roughly queue order.
		now := time.Now()
		for len(pending) > 0 && inflight < workers && !pending[0].notBefore.After(now) {
			j := pending[0]
			pending = pending[1:]
			inflight++
			go s.work(ctx, j, ch)
		}

		if inflight == 0 {
			// only cooling-down entries are left
			wait := max(100*time.Millisecond, time.Until(pending[0].notBefore))
			if nap(ctx, wait) != nil {
				// the run is over. stop honoring cooldowns rather than spinning on
				// a dead context: the remaining sources fail fast and are recorded
				// with their errors.
				for i := range pending {
					pending[i].notBefore = time.Time{}
				}
			}
			continue
		}

		// python waits on FIRST_COMPLETED with a 0.5s timeout, then handles every
		// future that is already done — so the timeout is what lets the cooldown
		// check above run while work is still in flight.
		select {
		case d := <-ch:
			inflight--
			pending = s.collect(d, pending)
		case <-time.After(500 * time.Millisecond):
			continue
		}
		for {
			select {
			case d := <-ch:
				inflight--
				pending = s.collect(d, pending)
				continue
			default:
			}
			break
		}
	}
}

// work runs one source. the timer starts here rather than at dispatch so a
// source is not charged for time it spent queued.
func (s *sched) work(ctx context.Context, j job, ch chan<- done) {
	st := s.state(j.src.Name)
	st.Start()
	d := done{job: j, st: st}
	defer func() {
		// python catches every exception out of fetch_source. a panic here would
		// otherwise take the other 149 sources down with it.
		if v := recover(); v != nil {
			d.recs, d.errs = nil, []string{fmt.Sprintf("%s: crashed: %v", j.src.Name, v)}
		}
		d.elapsed = st.Snapshot().Elapsed
		ch <- d
	}()
	d.recs, d.errs = s.p.F.Source(ctx, j.src, st)
}

// collect records a completion, or puts the source back on the queue when it
// was rate limited and has requeues left.
func (s *sched) collect(d done, pending []job) []job {
	if d.job.attempts < MaxRequeues && hasRateLimit(d.errs) {
		s.logf("%-24s 429 -> requeued (%d/%d)", d.job.src.Name,
			d.job.attempts+1, MaxRequeues)
		return append(pending, job{
			src:       d.job.src,
			attempts:  d.job.attempts + 1,
			notBefore: time.Now().Add(RequeueCooldown),
		})
	}
	snap := d.st.Snapshot()
	s.finish(d.job.src.Name, &outcome{
		recs: d.recs, requests: snap.Requests,
		elapsed: d.elapsed, requeues: d.job.attempts,
	}, d.errs)
	s.logf("%-24s %7d recs  %4d reqs  %5.1fs", d.job.src.Name, len(d.recs),
		snap.Requests, d.elapsed.Seconds())
	return pending
}

// hasRateLimit is python's `any("429" in e for e in errs)`. it is a substring
// test on the whole message, so a source whose url happens to contain 429 gets
// requeued too — with a 20s cooldown and a cap of 2, harmlessly.
func hasRateLimit(errs []string) bool {
	for _, e := range errs {
		if strings.Contains(e, "429") {
			return true
		}
	}
	return false
}

// watch names any source that has been running longer than slowAfter, and
// keeps naming it every slowAfter until it finishes.
//
// a requeued source is still in the progress map while it cools down, holding
// the start time of its previous attempt, so it gets reported as slow through
// the cooldown. python does the same.
func (s *sched) watch(stop <-chan struct{}) {
	announced := map[string]time.Time{}
	for {
		now := time.Now()
		for _, name := range s.pending() {
			st, ok := s.pendingState(name)
			if !ok {
				continue
			}
			sn := st.Snapshot()
			last := announced[name]
			if sn.Elapsed > slowAfter && (last.IsZero() || now.Sub(last) >= slowAfter) {
				announced[name] = now
				s.logf("[slow %5.0fs] %s: reqs=%d page=%s url=%s",
					sn.Elapsed.Seconds(), name, sn.Requests, dash(sn.Page), dashStr(sn.URL))
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(watchTick):
		}
	}
}

func (s *sched) pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// pendingState returns a name's state unless it has already finished.
func (s *sched) pendingState(name string) (*State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.res[name] != nil {
		return nil, false
	}
	return s.states[name], s.states[name] != nil
}

// dash renders `st.get("page") or '-'`. a page of 0 is a dash, which is what
// python prints for the two offset-paginated sources that start there.
func dash(page int) string {
	if page == 0 {
		return "-"
	}
	return strconv.Itoa(page)
}

func dashStr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
