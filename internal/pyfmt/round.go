// package pyfmt reproduces python's numeric formatting semantics.
//
// python's round() is round-half-to-even; go's math.Round is
// half-away-from-zero. every site where the python called round() has to go
// through here or scores and readme percentages drift by one on exact ties.
package pyfmt

import (
	"math"
	"strconv"
	"strings"
)

// Round matches python's round(x) -> int.
func Round(x float64) int {
	return int(math.RoundToEven(x))
}

// RoundN matches python's round(x, n) -> float.
//
// python rounds the exact decimal expansion of the binary double, half to
// even. strconv.FormatFloat does the same, so format-then-reparse is exact
// where naive scaling by 10^n is not.
func RoundN(x float64, n int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	v, err := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
	if err != nil {
		return x
	}
	return v
}

// Float renders a float the way python's json.dumps does, via repr(): the
// shortest string that round-trips, but always carrying a decimal point so an
// integral value emits "1.0" rather than "1".
//
// the only floats reaching the output are reliability and quality, both in
// [0,1] rounded to 4 places, so the exponent form python would use below 1e-4
// is unreachable and 'f' is safe.
func Float(x float64) string {
	if math.IsInf(x, 1) {
		return "Infinity"
	}
	if math.IsInf(x, -1) {
		return "-Infinity"
	}
	if math.IsNaN(x) {
		return "NaN"
	}
	s := strconv.FormatFloat(x, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
