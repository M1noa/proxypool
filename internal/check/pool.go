package check

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

// SkipKey is one known-dead protocol probe to skip re-testing, keyed the same
// way lib.history.skip_keys builds its (ip, port, proto) tuples.
type SkipKey struct {
	IP    string
	Port  int
	Proto string
}

// AliveKey identifies a record by ip:port only, for the second-chance pass:
// prev_alive carries no protocol, just a history of having answered at all.
type AliveKey struct {
	IP   string
	Port int
}

// Outcome is one protocol actually probed for one record. RT is nil when it
// failed, matching outcomes' `(ip, port, proto, alive, rt_ms|None)` tuples.
type Outcome struct {
	IP    string
	Port  int
	Proto string
	Alive bool
	RT    *int
}

// Stats is check_all's run summary.
type Stats struct {
	Total      int
	Alive      int
	Dead       int
	Skipped    int
	Revived    int
	BaselineMS int
}

// Options configures one CheckAll run. the zero value derives concurrency,
// runs the speedtest, skips nothing, and discards log lines.
type Options struct {
	Concurrency int // 0 = derive from cpu/ram/bandwidth
	Skip        map[SkipKey]bool
	Speedtest   bool
	PrevAlive   map[AliveKey]bool
	Logf        func(format string, args ...any)
	// Timeout caps one probe. zero uses the 5s default.
	Timeout time.Duration
}

// job pairs a record with the plan check_all decided to run for it, after
// skip filtering. python stores this as rec["_plan"]; here it travels
// alongside the record instead of living on it.
type job struct {
	rec  *extract.Record
	plan []string
}

// deriveConcurrency is _derive_concurrency: cpu and free ram set the ceiling
// since the checker is latency-bound, bandwidth only pulls it down on a thin
// pipe. weights and the [concurrencyMin, concurrencyMax] clamp are load-bearing
// for output stability and are kept byte-for-byte.
func deriveConcurrency(mbps float64) int {
	cpus := runtime.NumCPU()
	if cpus <= 0 {
		cpus = 2
	}
	cpuBudget := float64(cpus) * 600
	ramBudget := float64(concurrencyDefault)
	if vm, err := mem.VirtualMemory(); err == nil {
		// available, not total: the record set is already resident by now
		ramBudget = float64(vm.Available) / 1e9 * 500
	}
	netBudget := cpuBudget
	if mbps > 0 {
		netBudget = mbps * 4
	}
	blend := 0.45*cpuBudget + 0.25*ramBudget + 0.30*netBudget
	if blend == 0 {
		return concurrencyDefault
	}
	return max(concurrencyMin, min(concurrencyMax, pyfmt.Round(blend)))
}

// raiseFDLimit is _raise_fd_limit: each worker can hold a gate socket plus
// one per parallel probe, well past the default soft limit. errors are
// ignored, same as python's bare except — Setrlimit fails outright on some
// platforms for a want above the kernel's real ceiling.
func raiseFDLimit(concurrency int) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return
	}
	want := uint64(concurrency*6 + 1024)
	if want < rl.Cur {
		want = rl.Cur
	}
	if want > rl.Max {
		want = rl.Max
	}
	if want > rl.Cur {
		rl.Cur = want
		syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rl)
	}
}

// CheckAll is check_all: verify every record's claimed protocols, drop the
// dead ones, and report run-level stats plus every outcome actually probed.
// ctx's deadline, if any, is check_all's `deadline` backstop — the pool is
// cut short there and whatever was probed so far is still returned.
func CheckAll(ctx context.Context, records []*extract.Record, opts Options) (alive []*extract.Record, stats Stats, outcomes []Outcome, skipped int) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	var kept []job
	for _, r := range records {
		plan := probePlan(r)
		if len(opts.Skip) > 0 {
			filtered := make([]string, 0, len(plan))
			for _, p := range plan {
				if !opts.Skip[SkipKey{r.IP, r.Port, p}] {
					filtered = append(filtered, p)
				}
			}
			plan = filtered
		}
		if len(plan) == 0 {
			skipped++
			continue
		}
		kept = append(kept, job{rec: r, plan: plan})
	}
	// shuffle so dead-heavy source clusters don't skew the running rate/eta
	rand.Shuffle(len(kept), func(i, j int) { kept[i], kept[j] = kept[j], kept[i] })

	c := &Checker{timeout: opts.Timeout}
	if err := c.calibrate(ctx); err != nil {
		logf("checker: %v", err)
		return nil, Stats{}, nil, skipped
	}

	concurrency := opts.Concurrency
	if concurrency == 0 {
		mbps := 0.0
		if opts.Speedtest {
			mbps = c.measureMbps(ctx)
		}
		concurrency = deriveConcurrency(mbps)
	}

	spec := fmt.Sprintf("%dcpu", runtime.NumCPU())
	if vm, err := mem.VirtualMemory(); err == nil {
		spec += fmt.Sprintf(" %.1fgb free", float64(vm.Available)/1e9)
	}
	if c.mbps > 0 {
		logf("concurrency=%d (%s, %.0f mbps)", concurrency, spec, c.mbps)
	} else {
		logf("concurrency=%d (%s, no speedtest)", concurrency, spec)
	}

	raiseFDLimit(concurrency)

	cnt := &counters{}
	results := make([]bool, len(kept))
	planOutcomes := make([][]probeOutcome, len(kept))

	stopProgress := startProgress(ctx, len(kept), cnt, concurrency, logf)
	runPool(ctx, c, kept, results, planOutcomes, concurrency, cnt, logf)
	stopProgress()
	logf("pool done: %d/%d checked", cnt.checked.Load(), len(kept))

	// second chance: recently-alive proxies that just failed get one
	// re-probe. no reporter and counters don't move — python's track=None.
	revived := 0
	var retryIdx []int
	if len(opts.PrevAlive) > 0 {
		for i, j := range kept {
			if !results[i] && opts.PrevAlive[AliveKey{j.rec.IP, j.rec.Port}] {
				retryIdx = append(retryIdx, i)
			}
		}
	}
	if len(retryIdx) > 0 {
		logf("second-chance: re-probing %d recently-alive failures", len(retryIdx))
		retry := make([]job, len(retryIdx))
		for j, i := range retryIdx {
			retry[j] = kept[i]
		}
		retryResults := make([]bool, len(retryIdx))
		retryOutcomes := make([][]probeOutcome, len(retryIdx))
		runPool(ctx, c, retry, retryResults, retryOutcomes, concurrency, nil, logf)
		for j, ok := range retryResults {
			if ok {
				i := retryIdx[j]
				results[i] = true
				planOutcomes[i] = retryOutcomes[j]
				revived++
			}
		}
		logf("second-chance: revived %d/%d", revived, len(retryIdx))
	}

	logf("aggregating outcomes")
	for i, j := range kept {
		for _, po := range planOutcomes[i] {
			outcomes = append(outcomes, Outcome{IP: j.rec.IP, Port: j.rec.Port, Proto: po.Proto, Alive: po.RT != nil, RT: po.RT})
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	for i, j := range kept {
		if results[i] {
			ts := now
			j.rec.LastChecked = &ts
			alive = append(alive, j.rec)
		}
	}

	stats = Stats{
		Total:      len(kept),
		Alive:      len(alive),
		Dead:       len(kept) - len(alive),
		Skipped:    skipped,
		Revived:    revived,
		BaselineMS: pyfmt.Round(c.baseline),
	}
	return alive, stats, outcomes, skipped
}

// runPool is run_pool: concurrency workers pull indices off a shared cursor
// until it runs past len(jobs). ctx's deadline is the backstop against a
// probe that outlived its own recordCap guard; results/outcomes are
// pre-sized and written by disjoint index, so a cut-short run still returns
// whatever finished. cnt is nil for the second-chance pass.
func runPool(ctx context.Context, c *Checker, jobs []job, results []bool, outcomes [][]probeOutcome, concurrency int, cnt *counters, logf func(string, ...any)) {
	if len(jobs) == 0 || ctx.Err() != nil {
		return
	}

	var cursor atomic.Int64
	var wg sync.WaitGroup
	done := make(chan struct{})

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(cursor.Add(1)) - 1
				if i >= len(jobs) {
					return
				}
				rctx, cancel := context.WithTimeout(ctx, recordCap)
				ok, po := c.checkOne(rctx, jobs[i].rec, jobs[i].plan)
				cancel()
				results[i] = ok
				outcomes[i] = po
				if cnt != nil {
					cnt.checked.Add(1)
					if ok {
						cnt.alive.Add(1)
					} else {
						cnt.dead.Add(1)
					}
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		<-done // rctx derives from ctx, so in-flight probes abort fast once it cancels
		checked := "?"
		if cnt != nil {
			checked = strconv.FormatInt(cnt.checked.Load(), 10)
		}
		logf("time budget hit, stopping with %s/%d probed", checked, len(jobs))
	}
}
