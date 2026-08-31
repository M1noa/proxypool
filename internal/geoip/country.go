package geoip

import (
	_ "embed"
	"strings"
	"sync"
	"unicode"
)

// countriesData is every key pycountry.countries.lookup() accepts, mapped to
// the alpha-2 it resolves to. Generated from pycountry's own lookup indices
// (alpha_2, alpha_3, flag, name, numeric, official_name, common_name) with
// earlier fields winning collisions, which is the order lookup() searches -
// so this table resolves exactly what the python did, by construction.
//
//go:embed countries.txt
var countriesData string

var (
	countriesOnce sync.Once
	countries     map[string]string
)

func countryIndex() map[string]string {
	countriesOnce.Do(func() {
		lines := strings.Split(strings.TrimSuffix(countriesData, "\n"), "\n")
		countries = make(map[string]string, len(lines))
		for _, line := range lines {
			if key, iso, ok := strings.Cut(line, "\t"); ok {
				countries[key] = iso
			}
		}
	})
	return countries
}

// CountryToISO is fetch_proxies.py's country_to_iso: resolve a country name to
// its alpha-2 code, retrying on the leading segment for values like
// "France - Lauterbourg" or "Seoul, South Korea". Returns "" when neither
// attempt resolves, matching the python's LookupError fallthrough.
func CountryToISO(name string) string {
	if name == "" {
		return ""
	}
	idx := countryIndex()
	if iso, ok := idx[strings.ToLower(name)]; ok {
		return iso
	}
	head := name
	if i := strings.Index(head, " - "); i != -1 {
		head = head[:i]
	}
	if i := strings.Index(head, ","); i != -1 {
		head = head[:i]
	}
	return idx[strings.ToLower(pyStrip(head))]
}

// pyStrip is str.strip(), the same predicate as extract.PyStrip - inlined
// rather than imported so internal/geoip stays independent of the extraction
// layer. unicode.IsSpace omits \x1c-\x1f, which python counts as whitespace.
func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return (r >= 0x1c && r <= 0x1f) || unicode.IsSpace(r)
	})
}
