package extract

import (
	"errors"
	"regexp"

	"github.com/M1noa/proxypool/internal/config"
)

// extractText ports the text branch of parse_content's group generator.
//
// finditer scans the whole body unanchored; the default walks lines and
// anchors each one, which is why a source whose rows carry a trailing comment
// needs finditer to see them at all.
//
// only five keys come out regardless of what the pattern names: the group is
// `proto` but the record field is `protocol`, the country lands in both
// `country` and `meta_country`, and any other named group is discarded.
func extractText(content string, ex *config.Extract) ([]map[string]any, error) {
	if ex.Regex == "" {
		// python indexes ex["regex"] and the KeyError surfaces as
		// "<name>: parse failed: 'regex'"
		return nil, errors.New("'regex'")
	}
	var out []map[string]any
	add := func(g map[string]any) {
		out = append(out, map[string]any{
			"ip":           g["ip"],
			"port":         g["port"],
			"country":      g["country"],
			"protocol":     g["proto"],
			"meta_country": g["country"],
		})
	}

	if ex.Finditer {
		re, err := regexp.Compile(ex.Regex)
		if err != nil {
			return nil, err
		}
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			add(groupDict(re, m))
		}
		return out, nil
	}
	re, err := compileMatch(ex.Regex)
	if err != nil {
		return nil, err
	}
	for _, line := range pySplitLines(content) {
		if m := re.FindStringSubmatch(PyStrip(line)); m != nil {
			add(groupDict(re, m))
		}
	}
	return out, nil
}
