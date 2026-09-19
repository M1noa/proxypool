package uagen

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// uacore pulls extra template seeds from ua-parser/uap-core's test corpus.
// the file carries family + major inline, so freshness filtering needs no
// yaml parser: line regexes are enough.
//
// only entries whose family maps to a composed browser and whose major is
// within 2 of the current one survive. stale agents would poison a scraping
// pool.
func (f *fetcher) uacore(ctx context.Context, v versions) []wimbUA {
	body, ok := f.get(ctx, "uap-core",
		epUACore, false)
	if !ok {
		return nil
	}
	current := map[string]int{
		"Chrome":        majorOf(v.chromeStable["Windows"]),
		"Firefox":       majorOf(v.firefox),
		"Mobile Safari": majorOf(v.safariVer),
		"Safari":        majorOf(v.safariVer),
		"Edge":          majorOf(edgeNewest(v.edgeStable)),
		"Opera":         135, // no version api; validated against wimb at runtime
	}
	var out []wimbUA
	seen := map[string]bool{}
	for _, m := range reUACase.FindAllStringSubmatch(body, -1) {
		ua, family, majorS := m[1], m[2], m[3]
		if !strings.HasPrefix(ua, "Mozilla/5.0") || seen[ua] {
			continue
		}
		seen[ua] = true
		cur, ok := current[family]
		if !ok {
			continue
		}
		major, err := strconv.Atoi(majorS)
		if err != nil || cur-major > 2 || major-cur > 0 {
			continue
		}
		if w, ok := parseWimbUA(ua, "uap-core"); ok {
			w.source = "uap-core"
			out = append(out, w)
		}
	}
	return out
}

var reUACase = regexp.MustCompile(`(?m)- user_agent_string: '(.*)'\n\s*family: '(.*)'\n\s*major: '?(\d*)'?`)

func majorOf(ver string) int {
	n, _ := strconv.Atoi(strings.SplitN(ver, ".", 2)[0])
	return n
}

func edgeNewest(m map[string]string) string {
	for _, plat := range []string{"Windows", "MacOS", "Android"} {
		if v, ok := m[plat]; ok {
			return v
		}
	}
	for _, v := range m {
		return v
	}
	return ""
}
