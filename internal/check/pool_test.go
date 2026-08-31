package check

import (
	"context"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/extract"
)

func TestDeriveConcurrencyStaysWithinClamp(t *testing.T) {
	// deriveConcurrency reads live cpu/ram, so the exact blend varies by
	// machine; the [concurrencyMin, concurrencyMax] clamp must hold regardless.
	for _, mbps := range []float64{0, -5, 1, 100, 1e6} {
		got := deriveConcurrency(mbps)
		if got < concurrencyMin || got > concurrencyMax {
			t.Errorf("deriveConcurrency(%v) = %d, want within [%d, %d]", mbps, got, concurrencyMin, concurrencyMax)
		}
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
