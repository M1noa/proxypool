package config

import (
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
