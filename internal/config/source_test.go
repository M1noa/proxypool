package config

import (
	"encoding/json"
	"regexp"
	"strconv"
	"testing"
)

const sourcesPath = "../../sources.jsonc"

func load(t *testing.T) []Source {
	t.Helper()
	srcs, err := Load(sourcesPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return srcs
}

func TestLoadSources(t *testing.T) {
	srcs := load(t)
	t.Logf("%d sources", len(srcs))

	byFormat := map[string]int{}
	names := map[string]bool{}
	noURL := 0
	for _, s := range srcs {
		byFormat[s.Fmt()]++
		if names[s.Name] {
			t.Errorf("duplicate source name %q", s.Name)
		}
		names[s.Name] = true
		if s.URL == "" && len(s.URLs) == 0 {
			noURL++
			if s.Flow == "" {
				t.Errorf("%s: no url, no urls, no flow", s.Name)
			}
		}
	}
	t.Logf("formats: %v; %d without a url", byFormat, noURL)
}

// every regex in the config has to compile under RE2. python's re accepts
// lookaheads and backreferences that go rejects outright, so a bad one would
// otherwise surface as a dead source at runtime.
func TestRegexesAreRE2(t *testing.T) {
	check := func(where, rx string) {
		if rx == "" {
			return
		}
		if _, err := regexp.Compile(rx); err != nil {
			t.Errorf("%s: %v", where, err)
		}
	}
	for _, s := range load(t) {
		check(s.Name+".extract.regex", s.Extract.Regex)
		if e := s.Extract.EmbeddedJSON; e != nil {
			check(s.Name+".extract.embedded_json.regex", e.Regex)
		}
		for f, sp := range map[string]Spec{
			"ip": s.Extract.IP, "port": s.Extract.Port,
			"protocol": s.Extract.Protocol, "protocols": s.Extract.Protocols,
			"country": s.Extract.Country, "country_name": s.Extract.CountryName,
			"anonymity": s.Extract.Anonymity, "https": s.Extract.HTTPS,
			"response_time": s.Extract.ResponseTime,
		} {
			check(s.Name+".extract."+f+".regex", sp.Regex)
		}
		for k, sp := range s.Extract.SourceMeta {
			check(s.Name+".extract.source_meta."+k+".regex", sp.Regex)
		}
		for i, p := range s.Prefetch {
			check(s.Name+".prefetch["+strconv.Itoa(i)+"].regex", p.Regex)
		}
	}
}

// the text extractor reads named groups ip/port/country/proto out of the match,
// so a text source whose regex names none of them yields nothing.
func TestTextRegexesNameIPAndPort(t *testing.T) {
	for _, s := range load(t) {
		if s.Fmt() != "text" || s.Flow != "" || s.Extract.Regex == "" {
			continue
		}
		re, err := regexp.Compile(s.Extract.Regex)
		if err != nil {
			continue // reported by TestRegexesAreRE2
		}
		var hasIP, hasPort bool
		for _, n := range re.SubexpNames() {
			switch n {
			case "ip":
				hasIP = true
			case "port":
				hasPort = true
			}
		}
		if !hasIP || !hasPort {
			t.Errorf("%s: text regex names ip=%v port=%v", s.Name, hasIP, hasPort)
		}
	}
}

// the defaults are lib/parse.py's `or`/`.get(k, default)` fallbacks. they are
// invisible in sources.jsonc — 150 sources rely on them — so pin them here.
func TestDefaults(t *testing.T) {
	var s Source
	if got := s.Fmt(); got != "text" {
		t.Errorf("Fmt = %q, want text", got)
	}
	if got := s.HTTPMethod(); got != "GET" {
		t.Errorf("HTTPMethod = %q, want GET", got)
	}
	if got := s.Budget(); got != 80 {
		t.Errorf("Budget = %d, want 80", got)
	}
	if got := s.TimeoutS(); got != 12 {
		t.Errorf("TimeoutS = %d, want 12", got)
	}
	if got := s.Extract.RowSel(); got != "tr" {
		t.Errorf("RowSel = %q, want tr", got)
	}

	var p Pagination
	if got := p.StartAt(); got != 1 {
		t.Errorf("StartAt = %d, want 1", got)
	}
	if got := p.StepBy(); got != 1 {
		t.Errorf("StepBy = %d, want 1", got)
	}
	if got := p.PageSize(); got != 100 {
		t.Errorf("PageSize = %d, want 100", got)
	}
	if got := p.MaxPagesOr(7); got != 7 {
		t.Errorf("MaxPagesOr = %d, want the caller's 7", got)
	}
	if got := (PrefetchStep{}).GroupNum(); got != 1 {
		t.Errorf("GroupNum = %d, want 1", got)
	}

	// an explicit zero has to survive, not fall back
	zero := 0
	if got := (Pagination{Start: &zero}).StartAt(); got != 0 {
		t.Errorf("StartAt with explicit 0 = %d, want 0", got)
	}
}

// Spec is written either bare or as an object, and Empty has to agree with
// python's `if not spec` on both forms.
func TestSpecUnion(t *testing.T) {
	var ex Extract
	if err := json.Unmarshal([]byte(`{
		"ip": "a.b",
		"port": {"path": "p", "map": {}},
		"country": {"selector": "td", "attr": "title"},
		"anonymity": "",
		"https": {}
	}`), &ex); err != nil {
		t.Fatal(err)
	}

	if ex.IP.IsObject() || ex.IP.JSONPath() != "a.b" || ex.IP.CSSSelector() != "a.b" {
		t.Errorf("bare spec: %+v", ex.IP)
	}
	if !ex.Port.IsObject() || ex.Port.JSONPath() != "p" {
		t.Errorf("object spec: %+v", ex.Port)
	}
	if !ex.Port.HasMap() {
		t.Error("a present-but-empty map is not an absent one")
	}
	if ex.IP.HasMap() {
		t.Error("bare spec has no map")
	}
	if ex.Country.CSSSelector() != "td" || ex.Country.Attr != "title" {
		t.Errorf("html spec: %+v", ex.Country)
	}

	for name, s := range map[string]Spec{
		"absent":       ex.ResponseTime,
		"empty string": ex.Anonymity,
		"empty object": ex.HTTPS,
	} {
		if !s.Empty() {
			t.Errorf("%s spec should be empty: %+v", name, s)
		}
	}
	for name, s := range map[string]Spec{"bare": ex.IP, "object": ex.Port} {
		if s.Empty() {
			t.Errorf("%s spec should not be empty: %+v", name, s)
		}
	}
}

func TestEffectiveExtract(t *testing.T) {
	var s Source
	if err := json.Unmarshal([]byte(`{
		"source_meta": {"city": "city", "isp": "isp"},
		"extract": {"source_meta": {"isp": "nested.isp"}}
	}`), &s); err != nil {
		t.Fatal(err)
	}
	ex := s.EffectiveExtract()
	if got := ex.SourceMeta["city"].JSONPath(); got != "city" {
		t.Errorf("top-level key dropped: %q", got)
	}
	if got := ex.SourceMeta["isp"].JSONPath(); got != "nested.isp" {
		t.Errorf("extract.source_meta should win a collision, got %q", got)
	}
	if len(s.Extract.SourceMeta) != 1 {
		t.Error("the source's own extract must not be mutated")
	}

	// nothing to fold in: the source's own rules, unchanged
	var plain Source
	if plain.EffectiveExtract() != &plain.Extract {
		t.Error("want the extract itself when there is no top-level source_meta")
	}
}
