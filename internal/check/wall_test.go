package check

import (
	"context"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/extract"
)

// the wall: a record whose probes never come back and never check ctx must
// still release its worker. without it one wedged record held a 620k run
// from 01:11 to its 02:34 budget, and nothing published.
func TestRecordWall(t *testing.T) {
	origCap, origFn := recordCap, checkRecordFn
	recordCap = 200 * time.Millisecond
	release := make(chan struct{})
	checkRecordFn = func(*Checker, context.Context, *extract.Record, []string) (bool, []probeOutcome) {
		<-release // ignores cancellation entirely
		return true, nil
	}
	t.Cleanup(func() { recordCap, checkRecordFn = origCap, origFn; close(release) })

	rec := &extract.Record{IP: "203.0.113.1", Port: 8080}
	t0 := time.Now()
	ok, po := (&Checker{}).checkOne(context.Background(), rec, []string{"http", "https"})
	d := time.Since(t0)

	if ok {
		t.Errorf("wedged record reported alive")
	}
	if len(po) != 2 {
		t.Errorf("got %d outcomes, want one per planned probe", len(po))
	}
	if d > recordWall()+2*time.Second {
		t.Errorf("returned after %v, past the %v wall", d, recordWall())
	}
	t.Logf("returned after %v, wall %v", d, recordWall())
}
