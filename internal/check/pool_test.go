package check

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/extract"
)

func TestDeriveConcurrencyStaysWithinClamp(t *testing.T) {
	// deriveConcurrency reads live cpu/ram, so the exact budget varies by
	// machine; the [concurrencyMin, concurrencyMax] clamp must hold regardless.
	// a thin pipe or a tiny descriptor limit must not push it below the floor,
	// and an absurd one must not push it past the ceiling.
	for _, mbps := range []float64{0, -5, 1, 100, 1e6} {
		for _, fd := range []uint64{0, 1, fdReserve, fdReserve + 1, 1 << 20} {
			got := deriveConcurrency(mbps, fd)
			if got < concurrencyMin || got > concurrencyMax {
				t.Errorf("deriveConcurrency(%v, %d) = %d, want within [%d, %d]",
					mbps, fd, got, concurrencyMin, concurrencyMax)
			}
		}
	}
}

// raiseFDLimit must never claim more headroom than the kernel granted:
// deriveConcurrency divides by its return, so an overreport oversubscribes
// sockets and every probe that cannot get one is recorded as a dead proxy.
func TestRaiseFDLimitReportsWhatItGot(t *testing.T) {
	got := raiseFDLimit(concurrencyMax)
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		t.Skip(err)
	}
	if got > rl.Cur {
		t.Errorf("reported %d, soft limit is %d", got, rl.Cur)
	}
	if want := uint64(concurrencyMax)*fdPerWorker + fdReserve; got > want {
		t.Errorf("reported %d, only asked for %d", got, want)
	}
}

// TestCheckAllSkipFiltering exercises only the skip-filtering and skipped
// count that check_all computes before it ever touches the network — a
// record whose whole plan is filtered out by opts.Skip must be excluded from
// the pool and counted in skipped, and that return value is populated on
// every path, including the one where calibrate itself fails (e.g. no
// network in this sandbox). calibrate's own http calls to hardcoded upstream
// urls are exercised by the cli-level differential harness, not here.
func TestCheckAllSkipFiltering(t *testing.T) {
	orig, origCap := timeoutDefault, recordCap
	timeoutDefault, recordCap = 150*time.Millisecond, 500*time.Millisecond
	defer func() { timeoutDefault, recordCap = orig, origCap }()

	fullySkipped := rec() // default plan is [https, http]
	fullySkipped.IP, fullySkipped.Port = "1.2.3.4", 8080
	kept := rec()
	kept.IP, kept.Port = "5.6.7.9", 8081

	opts := Options{
		Concurrency: 1,
		Speedtest:   false,
		Skip: map[SkipKey]bool{
			{IP: "1.2.3.4", Port: 8080, Proto: "https"}: true,
			{IP: "1.2.3.4", Port: 8080, Proto: "http"}:  true,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stats, _ := CheckAll(ctx, []*extract.Record{fullySkipped, kept}, opts)
	if stats.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", stats.Skipped)
	}
}
