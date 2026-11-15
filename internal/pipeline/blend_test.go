package pipeline

import (
	"testing"

	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/history"
	"github.com/M1noa/proxypool/internal/reputation"
)

func i(v int) *int { return &v }

func TestBlendAveragesResponseAndTakesWorstStreak(t *testing.T) {
	r := &extract.Record{IP: "1.2.3.4", Port: 80, Protocols: []string{"http", "socks5"}}
	k := func(proto string) history.Key { return history.Key{IP: r.IP, Port: 80, Proto: proto} }
	scores := map[history.Key]history.Score{
		k("http"):   {Reliability: 0.8, Quality: 0.7, ChecksTotal: 5, ChecksOK: 4, AvgResponseMS: i(400), FailsStreak: 1},
		k("socks5"): {Reliability: 0.6, Quality: 0.5, ChecksTotal: 5, ChecksOK: 3, AvgResponseMS: i(1000), FailsStreak: 3},
	}

	f := blendScores(r, 0, scores, &reputation.Feed{})
	if f.AvgResponseMS == nil || *f.AvgResponseMS != 700 {
		t.Errorf("AvgResponseMS = %v, want 700", f.AvgResponseMS)
	}
	if f.FailsStreak != 3 {
		t.Errorf("FailsStreak = %d, want 3", f.FailsStreak)
	}
	if f.Reliability != 0.7 {
		t.Errorf("Reliability = %v, want 0.7", f.Reliability)
	}

	// a protocol with no measured time contributes nothing, so the mean stays
	// over the protocols that have one rather than being dragged toward zero
	scores[k("socks5")] = history.Score{Reliability: 1, Quality: 1, ChecksTotal: 1, ChecksOK: 1}
	f = blendScores(r, 0, scores, &reputation.Feed{})
	if f.AvgResponseMS == nil || *f.AvgResponseMS != 400 {
		t.Errorf("AvgResponseMS with one measured protocol = %v, want 400", f.AvgResponseMS)
	}

	// no protocol scored: nothing measured, so the key stays absent
	f = blendScores(r, 0, map[history.Key]history.Score{}, &reputation.Feed{})
	if f.AvgResponseMS != nil {
		t.Errorf("AvgResponseMS with no scores = %v, want nil", *f.AvgResponseMS)
	}
}
