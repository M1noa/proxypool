package output

import (
	"cmp"
	"math"
	"slices"
)

// SortRecords is sort_records: highest quality first, then fastest response
// time, ties broken by keeping the original (merge) order - python's sorted
// is stable, so this uses a stable sort to match.
//
// both `or 0` and `or inf` are python truthiness on the key values: a missing
// quality and a quality of exactly 0.0 sort identically, and a
// response_time_ms of exactly 0 sorts as if it were missing (inf), not as
// the fastest proxy.
//
// keys are decorated onto the slice up front rather than recomputed inside the
// comparator. a stable sort calls its comparator on the order of n*log^2(n)
// times, and under -skip-check n is every merged record, so computing the key
// in there means chasing two *Record pointers into a large scattered heap on
// every one of those calls.
func SortRecords(records []*Record) {
	keyed := make([]keyedRecord, len(records))
	for i, r := range records {
		keyed[i] = keyedRecord{sortKey(r), r}
	}
	slices.SortStableFunc(keyed, func(a, b keyedRecord) int {
		if a.key.negQuality != b.key.negQuality {
			return cmp.Compare(a.key.negQuality, b.key.negQuality)
		}
		if a.key.rtIsNil != b.key.rtIsNil {
			if a.key.rtIsNil {
				return 1 // present sorts before absent
			}
			return -1
		}
		return cmp.Compare(a.key.rt, b.key.rt)
	})
	for i, k := range keyed {
		records[i] = k.rec
	}
}

type keyedRecord struct {
	key recordSortKey
	rec *Record
}

type recordSortKey struct {
	negQuality float64
	rt         float64
	rtIsNil    bool
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
	return recordSortKey{negQuality: -q, rt: rt, rtIsNil: r.ResponseTimeMS == nil}
}
