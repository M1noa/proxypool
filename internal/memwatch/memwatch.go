// package memwatch samples the process's memory and, once it crosses a
// threshold, reports where that memory actually is before trying to give any of
// it back. the ordering is the point: an unexplained "freed 4gb" line tells you
// nothing about why 4gb was held, and a run that quietly recovers is how a leak
// survives to the next release.
package memwatch

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

const (
	sampleEvery = 5 * time.Second
	// report once the process holds this share of system ram. a healthy full run
	// peaks well under a quarter of a github runner's 16gb, so crossing half
	// means something is holding what it should have released.
	reportFrac = 0.5
	// re-report only after growing another share this size, so one high-water
	// plateau does not emit the same block every five seconds
	regrowFrac = 0.1
	// post-gc live-heap readings kept for the trend. three is the minimum that
	// can distinguish a rise from a single noisy sample.
	liveSamples = 5
)

// Watcher samples on a ticker until stopped.
type Watcher struct {
	Logf func(format string, args ...any)

	mu    sync.Mutex
	probe func() string
}

// Probe registers a component's own accounting, or clears it with nil.
// runtime.MemStats covers only what go allocated, and in this binary the memory
// worth worrying about is duckdb's. safe to call while the watcher is running,
// which it has to be: history opens partway through a run.
func (w *Watcher) Probe(f func() string) {
	w.mu.Lock()
	w.probe = f
	w.mu.Unlock()
}

// Start begins sampling and returns the stop function. it is safe to call stop
// more than once.
func (w *Watcher) Start(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go w.loop(ctx, done)
	return func() { once.Do(func() { close(done) }) }
}

func (w *Watcher) loop(ctx context.Context, done chan struct{}) {
	if w.Logf == nil {
		return
	}
	var total uint64
	if vm, err := mem.VirtualMemory(); err == nil {
		total = vm.Total
	}
	if total == 0 {
		return // no denominator, so no threshold to cross
	}
	// a failure here is not fatal: ms.Sys still bounds the go half, it just
	// cannot see the cgo half, and report says so rather than guessing
	proc, _ := process.NewProcess(int32(os.Getpid()))

	threshold := uint64(float64(total) * reportFrac)
	tick := time.NewTicker(sampleEvery)
	defer tick.Stop()

	var live []uint64 // heap in use sampled just after each gc
	var lastGC uint32

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
		}

		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)

		// HeapAlloc read right after a gc is the live set. read at any other
		// moment it also counts garbage not yet collected, which is what makes
		// "memory keeps growing" so easy to misdiagnose.
		if ms.NumGC != lastGC {
			lastGC = ms.NumGC
			live = append(live, ms.HeapAlloc)
			if len(live) > liveSamples {
				live = live[1:]
			}
		}

		rss, haveRSS := ms.Sys, false
		if proc != nil {
			if mi, err := proc.MemoryInfo(); err == nil {
				rss, haveRSS = mi.RSS, true
			}
		}
		if rss < threshold {
			continue
		}
		w.report(proc, rss, total, haveRSS, &ms, live)
		threshold = rss + uint64(float64(total)*regrowFrac)
	}
}

// report prints the breakdown, the leak verdict, and only then tries to release
// anything — a cleanup that runs first would erase the evidence for it.
func (w *Watcher) report(proc *process.Process, rss, total uint64, haveRSS bool, ms *runtime.MemStats, live []uint64) {
	w.Logf("memory: rss %s of %s system (%.0f%%), %d goroutines, %d gc cycles",
		human(rss), human(total), 100*float64(rss)/float64(total),
		runtime.NumGoroutine(), ms.NumGC)
	w.Logf("  go heap:  %s live, %s reserved (%s idle, %s already returned), %d objects",
		human(ms.HeapAlloc), human(ms.HeapSys), human(ms.HeapIdle), human(ms.HeapReleased), ms.HeapObjects)
	w.Logf("  go other: %s stacks, %s gc metadata, %s runtime, %s reserved in total",
		human(ms.StackSys), human(ms.GCSys), human(ms.MSpanSys+ms.MCacheSys+ms.BuckHashSys+ms.OtherSys), human(ms.Sys))

	switch {
	case !haveRSS:
		w.Logf("  cgo:      unknown, could not read process rss")
	case rss > ms.Sys:
		// this is the diagnostic line: the go runtime accounts for every byte it
		// allocated, so whatever rss holds beyond ms.Sys came from cgo, and the
		// only cgo in this binary is duckdb
		w.Logf("  cgo:      %s held outside the go runtime", human(rss-ms.Sys))
	}
	w.mu.Lock()
	probe := w.probe
	w.mu.Unlock()
	if probe != nil {
		if s := probe(); s != "" {
			w.Logf("  %s", s)
		}
	}
	w.Logf("  %s", diagnose(live))

	t0 := time.Now()
	debug.FreeOSMemory()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// same handle and same fallback as the reading that tripped the threshold:
	// pairing an rss that includes cgo against a go-only Sys would report a
	// release that never happened
	rssAfter := after.Sys
	if proc != nil {
		if mi, err := proc.MemoryInfo(); err == nil {
			rssAfter = mi.RSS
		}
	}
	if rssAfter < rss {
		w.Logf("  cleanup:  released %s, rss now %s (%.1fs)",
			human(rss-rssAfter), human(rssAfter), time.Since(t0).Seconds())
	} else {
		// nothing to give back means everything held is still reachable — for
		// the go half that is the definition of a leak, and for the cgo half it
		// means a component is holding it on purpose and go cannot help
		w.Logf("  cleanup:  released nothing, rss still %s (%.1fs) — all of it is still in use",
			human(rssAfter), time.Since(t0).Seconds())
	}
}

// diagnose reads the post-gc live-heap trend. memory a gc cannot reclaim is
// still reachable, which is what a go leak is; memory that falls back is
// allocation churn the collector had not caught up with yet.
func diagnose(live []uint64) string {
	if len(live) < 3 {
		return fmt.Sprintf("leak check: inconclusive, only %d post-gc samples so far", len(live))
	}
	first, last := live[0], live[len(live)-1]
	rising := true
	for i := 1; i < len(live); i++ {
		if live[i] <= live[i-1] {
			rising = false
			break
		}
	}
	switch {
	case rising && last > first+first/4:
		return fmt.Sprintf("leak check: POSSIBLE LEAK — live heap grew every gc, %s -> %s over %d cycles;"+
			" memory surviving collection is still referenced somewhere",
			human(first), human(last), len(live))
	case last > first:
		return fmt.Sprintf("leak check: no leak signal — live heap %s -> %s over %d cycles but not monotonic,"+
			" consistent with a working set that grew, not one that is stuck",
			human(first), human(last), len(live))
	default:
		return fmt.Sprintf("leak check: no leak signal — live heap %s -> %s over %d cycles, gc is reclaiming",
			human(first), human(last), len(live))
	}
}

func human(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2fgb", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0fmb", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%dkb", b/(1<<10))
	}
}
