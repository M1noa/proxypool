package extract

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/M1noa/proxypool/internal/config"
)

// TestParseParity replays raw bodies captured from live sources through
// ParseContent and compares against what lib/parse.parse_content produced for
// the same bytes. it is the differential gate for the extraction port.
//
// testdata/fixtures holds a gzipped capture so the gate keeps working after the
// python is gone. regenerate with tools/capture_fixtures.py and point
// PROXYPOOL_FIXTURES at the output to test against fresh bodies.
func TestParseParity(t *testing.T) {
	dir := os.Getenv("PROXYPOOL_FIXTURES")
	if dir == "" {
		dir = "testdata/fixtures"
	}
	expRaw, err := readFixture(dir, "expected.json")
	if err != nil {
		t.Skipf("no fixtures in %s (set PROXYPOOL_FIXTURES): %v", dir, err)
	}

	var expected map[string]struct {
		Defaults map[string]any   `json:"defaults"`
		Records  []map[string]any `json:"records"`
	}
	d := json.NewDecoder(bytes.NewReader(expRaw))
	d.UseNumber()
	if err := d.Decode(&expected); err != nil {
		t.Fatalf("expected.json: %v", err)
	}

	srcs, err := config.Load("../../sources.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*config.Source{}
	for i := range srcs {
		byName[srcs[i].Name] = &srcs[i]
	}

	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			src, ok := byName[name]
			if !ok {
				t.Fatalf("source %q not in sources.jsonc", name)
			}
			content, err := readFixture(dir, name+".raw")
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseContent(src, string(content), want.Defaults)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(got) != len(want.Records) {
				t.Fatalf("record count: got %d, want %d", len(got), len(want.Records))
			}
			for i := range got {
				g, w := canon(recordMap(got[i])), canon(want.Records[i])
				if !reflect.DeepEqual(g, w) {
					t.Fatalf("record %d:\n got %v\nwant %v", i, g, w)
				}
			}
		})
	}
}

// readFixture reads name, preferring a committed name.gz over a plain file left
// by a fresh capture.
func readFixture(dir, name string) ([]byte, error) {
	if f, err := os.Open(filepath.Join(dir, name+".gz")); err == nil {
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// recordMap renders a Record in the shape lib/parse emits, so the two can be
// compared field by field.
func recordMap(r *Record) map[string]any {
	provided := make([]any, 0, len(r.Provided))
	for f := range r.Provided {
		provided = append(provided, f)
	}
	meta := map[string]any{}
	for k, v := range r.SourceMeta {
		meta[k] = v
	}
	sources := make([]any, len(r.Sources))
	for i, s := range r.Sources {
		sources[i] = s
	}
	protos := make([]any, len(r.Protocols))
	for i, p := range r.Protocols {
		protos[i] = p
	}
	m := map[string]any{
		"ip":           r.IP,
		"ip_version":   r.IPVersion,
		"port":         r.Port,
		"protocols":    protos,
		"country":      r.Country,
		"country_name": r.CountryName,
		"anonymity":    r.Anonymity,
		"https":        r.HTTPS,
		"response_time": func() any {
			if r.ResponseTime == nil {
				return nil
			}
			return *r.ResponseTime
		}(),
		"sources":     sources,
		"source_meta": meta,
		"_provided":   provided,
	}
	return m
}

// canon flattens the type differences between the two decoders: every number
// becomes a float64 and every list of strings is sorted. the int-versus-float
// rendering of a source_meta value is a serialization concern and belongs to
// the output encoder's tests, not here.
func canon(v any) any {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return string(t)
		}
		return f
	case int:
		return float64(t)
	case float64:
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = canon(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = canon(vv)
		}
		sortAny(out)
		return out
	}
	return v
}

// sortAny orders a list of strings. protocols, sources and _provided are all
// order-insensitive: python builds two of them from a set.
func sortAny(xs []any) {
	for i := 1; i < len(xs); i++ {
		a, ok := xs[i].(string)
		if !ok {
			return
		}
		for j := i; j > 0; j-- {
			b := xs[j-1].(string)
			if b <= a {
				break
			}
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
