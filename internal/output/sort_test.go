package output

import "testing"

func rtRec(quality float64, hasCheck bool, rt *int) *Record {
	r := &Record{ResponseTimeMS: rt}
	if hasCheck {
		r.Check = &CheckFields{Quality: quality}
	}
	return r
}

func msp(n int) *int { return &n }

func TestSortRecordsQualityDescending(t *testing.T) {
	low := rtRec(0.1, true, nil)
	high := rtRec(0.9, true, nil)
	records := []*Record{low, high}
	SortRecords(records)
	if records[0] != high || records[1] != low {
		t.Error("SortRecords must put higher quality first")
	}
}

func TestSortRecordsZeroResponseTimeSortsAsSlowest(t *testing.T) {
	// python: response_time_ms == 0 is falsy, so `rt or inf` makes it inf,
	// but `rt is None` is still False for it - it must still sort BEFORE a
	// record whose response_time_ms is genuinely None.
	zero := rtRec(0.5, true, msp(0))
	nilRT := rtRec(0.5, true, nil)
	fast := rtRec(0.5, true, msp(10))

	records := []*Record{nilRT, zero, fast}
	SortRecords(records)

	if records[0] != fast {
		t.Errorf("fastest real response time must sort first, got order %+v", records)
	}
	if records[1] != zero {
		t.Errorf("response_time_ms=0 must sort before response_time_ms=nil (same effective rt, but 'is None' differs), got order %+v", records)
	}
	if records[2] != nilRT {
		t.Errorf("response_time_ms=nil must sort last, got order %+v", records)
	}
}

func TestSortRecordsMissingQualitySameAsZero(t *testing.T) {
	noCheck := rtRec(0, false, msp(50))
	zeroQuality := rtRec(0, true, msp(50))
	records := []*Record{noCheck, zeroQuality}
	SortRecords(records)
	// both have effective quality 0 and identical rt -> stable sort keeps
	// original order.
	if records[0] != noCheck || records[1] != zeroQuality {
		t.Error("missing Check and Check.Quality==0 must be treated identically for sorting (stable, original order kept)")
	}
}

func TestSortRecordsStableOnFullTie(t *testing.T) {
	a := rtRec(0.5, true, msp(100))
	b := rtRec(0.5, true, msp(100))
	c := rtRec(0.5, true, msp(100))
	records := []*Record{a, b, c}
	SortRecords(records)
	if records[0] != a || records[1] != b || records[2] != c {
		t.Error("SortRecords must be stable: identical keys must keep original order")
	}
}
