package pyfmt

import (
	"math"
	"testing"
)

// expectations are literal python 3 outputs: round() is half-to-even, and for a
// tie the deciding digit is the exact binary value, not the decimal literal.
func TestRound(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0.5, 0}, {1.5, 2}, {2.5, 2}, {3.5, 4},
		{-0.5, 0}, {-1.5, -2}, {-2.5, -2},
		{0.49999999999999994, 0},
		{1.4, 1}, {1.6, 2}, {0, 0},
	}
	for _, c := range cases {
		if got := Round(c.in); got != c.want {
			t.Errorf("Round(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// expectations are literal json.dumps() output. the encoder writes these bytes
// straight into proxies.json, so a drift here is a diff in the published file.
func TestFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{1, "1.0"}, // an integral value still carries the point
		{0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{2, "2.0"},
		{0.5, "0.5"},
		{0.6666, "0.6666"}, // reliability/quality shape: 4 places
		{0.0001, "0.0001"}, // the smallest value RoundN(_, 4) can produce
		{0.1, "0.1"},       // shortest round-trip, not 0.1000000000000000055
		{123456789, "123456789.0"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{math.NaN(), "NaN"},
	}
	for _, c := range cases {
		if got := Float(c.in); got != c.want {
			t.Errorf("Float(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRoundNNonFinite(t *testing.T) {
	if got := RoundN(math.Inf(1), 4); !math.IsInf(got, 1) {
		t.Errorf("RoundN(+Inf) = %v", got)
	}
	if got := RoundN(math.NaN(), 4); !math.IsNaN(got) {
		t.Errorf("RoundN(NaN) = %v", got)
	}
}

func TestRoundN(t *testing.T) {
	cases := []struct {
		in   float64
		n    int
		want float64
	}{
		{2.675, 2, 2.67}, // 2.675 is really 2.67499...
		{0.125, 2, 0.12}, // exact tie, rounds to even
		{0.135, 2, 0.14}, // really 0.13500...0888
		{1.0005, 3, 1.0}, // really 1.00049999...
		{0.66665, 4, 0.6666},
		{0.12345, 4, 0.1235},
		{2.5, 0, 2.0},
		{1.0, 4, 1.0},
		{0.30000000000000004, 4, 0.3},
	}
	for _, c := range cases {
		if got := RoundN(c.in, c.n); got != c.want {
			t.Errorf("RoundN(%v, %d) = %v, want %v", c.in, c.n, got, c.want)
		}
	}
}
