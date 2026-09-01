package check

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// progressInterval is _progress_reporter's default interval=10. a var, not a
// const, so tests can shrink it instead of waiting out a real 10s tick.
var progressInterval = 10 * time.Second

// counters is the shared tally a progress reporter reads and a pool writes,
// as atomics rather than python's gil-protected dict.
type counters struct {
	checked atomic.Int64
	alive   atomic.Int64
	dead    atomic.Int64
}

// startProgress is _progress_reporter: it sleeps a full interval before its
// first log line, so the first one lands at t=interval, not t=0. returns a
// stop func that blocks until the reporter goroutine has exited; python
// cancels the asyncio task instead.
func startProgress(ctx context.Context, total int, c *counters, concurrency int, logf func(string, ...any)) func() {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		t0 := time.Now()
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()

		var lastNet uint64
		haveNet := false
		if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
			lastNet = io[0].BytesSent + io[0].BytesRecv
			haveNet = true
		}
		cpu.Percent(0, false) // prime; first call is meaningless

		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			elapsed := time.Since(t0).Seconds()
			checked := c.checked.Load()
			rate := 0.0
			if elapsed > 0 {
				rate = float64(checked) / elapsed
			}
			eta := "--"
			if rate > 0 {
				etaS := float64(int64(total)-checked) / rate
				eta = fmt.Sprintf("%.1fmin", etaS/60)
			}

			var cpuStr, netStr string
			if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
				cpuStr = fmt.Sprintf("%.0f%%", pct[0])
			} else if l, err := load.Avg(); err == nil {
				cpuStr = fmt.Sprintf("load %.1f", l.Load1)
			}
			if haveNet {
				if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
					now := io[0].BytesSent + io[0].BytesRecv
					d := int64(now) - int64(lastNet)
					lastNet = now
					netStr = fmt.Sprintf(" net %.0fmbps", float64(d)/progressInterval.Seconds()*8/1e6)
				}
			}

			logf("[check %6.0fs] %d/%d (%.0f/s avg) alive=%d dead=%d eta=%s cpu=%s%s conc=%d",
				elapsed, checked, total, rate, c.alive.Load(), c.dead.Load(), eta, cpuStr, netStr, concurrency)
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}
