package pipeline

import (
	"reflect"
	"testing"

	"github.com/M1noa/proxypool/internal/extract"
)

func f64(v float64) *float64 { return &v }

func TestMergeUnionsAndKeepsStrongest(t *testing.T) {
	a := &extract.Record{
		IP: "1.2.3.4", Port: 80,
		Protocols: []string{"http"}, Sources: []string{"b"},
		Anonymity: "transparent", ResponseTime: f64(900),
		SourceMeta: map[string]any{"isp": "one"},
		Provided:   map[string]bool{"port": true},
	}
	b := &extract.Record{
		IP: "1.2.3.4", Port: 80,
		Protocols: []string{"socks5", "http"}, Sources: []string{"a"},
		Anonymity: "elite", HTTPS: true, ResponseTime: f64(120),
		Country: "US", CountryName: "United States",
		SourceMeta: map[string]any{"isp": "two", "city": "NYC"},
		Provided:   map[string]bool{"anonymity": true},
	}
	// distinct key: must survive untouched alongside the merged pair
	c := &extract.Record{IP: "1.2.3.4", Port: 8080, Protocols: []string{"http"},
		SourceMeta: map[string]any{}, Provided: map[string]bool{}}

	out := merge([]*extract.Record{a, b, c})
	if len(out) != 2 {
		t.Fatalf("merge() kept %d records, want 2", len(out))
	}
	// first-seen order is what sort_records' stable sort rides on
	if out[0] != a || out[1] != c {
		t.Errorf("merge() reordered records")
	}
	got := out[0]
	if !reflect.DeepEqual(got.Protocols, []string{"http", "socks5"}) {
		t.Errorf("protocols = %v", got.Protocols)
	}
	if !reflect.DeepEqual(got.Sources, []string{"a", "b"}) {
		t.Errorf("sources = %v", got.Sources)
	}
	if got.Anonymity != "elite" {
		t.Errorf("anonymity = %q, want elite", got.Anonymity)
	}
	if !got.HTTPS {
		t.Error("https not or-ed in")
	}
	if got.Country != "US" || got.CountryName != "United States" {
		t.Errorf("country = %q/%q", got.Country, got.CountryName)
	}
	if got.ResponseTime == nil || *got.ResponseTime != 120 {
		t.Errorf("response_time = %v, want fastest 120", got.ResponseTime)
	}
	// first writer of a key wins; every key is kept
	if got.SourceMeta["isp"] != "one" || got.SourceMeta["city"] != "NYC" {
		t.Errorf("source_meta = %v", got.SourceMeta)
	}
	if !got.Provided["port"] || !got.Provided["anonymity"] {
		t.Errorf("_provided = %v", got.Provided)
	}
	if got.LastChecked != nil {
		t.Error("last_checked should be cleared for the checker phase")
	}
}

func TestMergeKeepsNilResponseTime(t *testing.T) {
	a := &extract.Record{IP: "9.9.9.9", Port: 1, SourceMeta: map[string]any{}, Provided: map[string]bool{}}
	b := &extract.Record{IP: "9.9.9.9", Port: 1, SourceMeta: map[string]any{}, Provided: map[string]bool{}}
	out := merge([]*extract.Record{a, b})
	if out[0].ResponseTime != nil {
		t.Errorf("response_time = %v, want nil", out[0].ResponseTime)
	}
}

func TestFinalize(t *testing.T) {
	recs := []*extract.Record{
		// name but no code: resolved through country_to_iso, then the name is dropped
		{IP: "1.1.1.1", Port: 1, CountryName: "Germany", ResponseTime: f64(123.9)},
		// no country at all
		{IP: "2.2.2.2", Port: 2},
		// an existing code is left alone
		{IP: "3.3.3.3", Port: 3, Country: "JP", CountryName: "Japan"},
	}
	out := finalize(recs)

	if out[0].Country != "DE" {
		t.Errorf("country = %q, want DE", out[0].Country)
	}
	// python int(): truncates toward zero, it does not round
	if out[0].ResponseTimeMS == nil || *out[0].ResponseTimeMS != 123 {
		t.Errorf("response_time_ms = %v, want 123", out[0].ResponseTimeMS)
	}
	if out[1].ResponseTimeMS != nil {
		t.Errorf("response_time_ms = %v, want nil", out[1].ResponseTimeMS)
	}
	if out[2].Country != "JP" {
		t.Errorf("country = %q, want JP", out[2].Country)
	}
	for i, r := range out {
		if r.CountryName != "" {
			t.Errorf("record %d kept country_name %q", i, r.CountryName)
		}
		if r.ResponseTime != nil || r.ResponseTimeRawMS != nil {
			t.Errorf("record %d did not clear the raw response times", i)
		}
	}
}

func TestSortedUnion(t *testing.T) {
	got := sortedUnion([]string{"c", "a", "c"}, []string{"b", "a"})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sortedUnion = %v", got)
	}
	if got := sortedUnion(nil, nil); len(got) != 0 {
		t.Errorf("sortedUnion(nil, nil) = %v", got)
	}
}
