package output

import (
	"math"
	"sort"
)

// SortRecords is sort_records: highest quality first, then fastest response
// time, ties broken by keeping the original (merge) order - python's sorted
// is stable, so this uses SliceStable to match.
//
// both `or 0` and `or inf` are python truthiness on the key values: a missing
// quality and a quality of exactly 0.0 sort identically, and a
// response_time_ms of exactly 0 sorts as if it were missing (inf), not as
// the fastest proxy.
func SortRecords(records []*Record) {
	sort.SliceStable(records, func(i, j int) bool {
		ki, kj := sortKey(records[i]), sortKey(records[j])
		if ki.negQuality != kj.negQuality {
			return ki.negQuality < kj.negQuality
		}
		if ki.rtIsNil != kj.rtIsNil {
			return !ki.rtIsNil // present (false) sorts before absent (true)
		}
		return ki.rt < kj.rt
	})
}

type recordSortKey struct {
	negQuality float64
	rtIsNil    bool
	rt         float64
}

func sortKey(r *Record) recordSortKey {
	q := 0.0
	if r.Check != nil && r.Check.Quality != 0 {
		q = r.Check.Quality
	}
	rt := math.Inf(1)
	if r.ResponseTimeMS != nil && *r.ResponseTimeMS != 0 {
		rt = float64(*r.ResponseTimeMS)
	}
	return recordSortKey{negQuality: -q, rtIsNil: r.ResponseTimeMS == nil, rt: rt}
}
