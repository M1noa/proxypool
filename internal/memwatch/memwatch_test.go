package memwatch

import (
	"strings"
	"testing"
)

// the verdict is the only thing in here worth being wrong about: calling churn a
// leak sends someone hunting a bug that isn't there, and calling a leak churn is
// how the last one survived to production.
func TestDiagnoseFlagsOnlyMonotonicGrowth(t *testing.T) {
	mb := func(n uint64) uint64 { return n << 20 }
	for _, c := range []struct {
		name string
		live []uint64
		leak bool
	}{
		{"not enough samples to have a trend", []uint64{mb(100), mb(900)}, false},
		{"flat", []uint64{mb(100), mb(100), mb(100)}, false},
		{"grew, then a gc got it back", []uint64{mb(100), mb(900), mb(120)}, false},
		{"rising but only just", []uint64{mb(100), mb(105), mb(110)}, false},
		{"rising every cycle", []uint64{mb(100), mb(400), mb(900)}, true},
	} {
		got := diagnose(c.live)
		if strings.Contains(got, "POSSIBLE LEAK") != c.leak {
			t.Errorf("%s: want leak=%v, got %q", c.name, c.leak, got)
		}
	}
}
