package uagen

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// shares holds statcounter usage weights: browser-version and os shares per
// device class, plus the device split itself. all values are fractions.
// families rolls versions up to browser family per device, so records match
// a weight even when the 6-row snapshot names no exact version.
type shares struct {
	versions map[string]map[string]float64 // device -> label -> share
	oses     map[string]map[string]float64 // device -> label -> share
	devices  map[string]float64            // desktop/mobile/tablet
	families map[string]map[string]float64 // device -> family -> share
}

var (
	// <th>Chrome 151.0</th><td><span class="count">9.29</span>
	reShareRow = regexp.MustCompile(`<th>([^<]+)</th>\s*<td><span class="count">([\d.]+)</span>`)
)

// table parses one statcounter snapshot table into label -> fraction.
func table(body string) map[string]float64 {
	out := map[string]float64{}
	for _, m := range reShareRow.FindAllStringSubmatch(body, -1) {
		pct, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		out[m[1]] = pct / 100
	}
	return out
}

// devKey normalizes a statcounter device label to desktop/mobile/tablet.
func devKey(label string) string {
	switch strings.ToLower(label) {
	case "desktop":
		return "desktop"
	case "mobile":
		return "mobile"
	case "tablet":
		return "tablet"
	}
	return ""
}

// familyWeight rolls version-row shares up to browser family: the snapshot
// only carries ~6 rows, so exact version labels match nothing most runs.
// "Chrome 151.0" -> chrome, "Edge 151" -> edge; "Samsung" keeps its own key
// since statcounter spells it out, and android/ios-flavored chrome rows count
// for chrome too.
func familyWeight(rows map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for label, share := range rows {
		l := strings.ToLower(label)
		fam := ""
		switch {
		case strings.Contains(l, "samsung"):
			fam = "samsung"
		case strings.HasPrefix(l, "chrome"):
			fam = "chrome"
		case strings.HasPrefix(l, "edge"):
			fam = "edge"
		case strings.HasPrefix(l, "firefox"):
			fam = "firefox"
		case strings.HasPrefix(l, "safari"):
			fam = "safari"
		case strings.HasPrefix(l, "opera"):
			fam = "opera"
		case strings.HasPrefix(l, "uc browser"):
			fam = "uc"
		case strings.HasPrefix(l, "android"):
			fam = "android"
		}
		if fam != "" {
			out[fam] += share
		}
	}
	return out
}

// shares fetches the statcounter tables: browser families, versions and oses
// per device, plus the device split. year urls serve the latest full month.
func (f *fetcher) shares(ctx context.Context) shares {
	s := shares{
		versions: map[string]map[string]float64{},
		oses:     map[string]map[string]float64{},
		devices:  map[string]float64{},
		families: map[string]map[string]float64{},
	}
	year := fmt.Sprintf("%d", f.now.Year())
	base := epStatcounter
	for _, dev := range []string{"desktop", "mobile", "tablet"} {
		if body, ok := f.get(ctx, "sc-browsers/"+dev,
			base+"/browser-market-share/"+dev+"/worldwide/"+year, true); ok {
			s.families[dev] = table(body)
		}
		if body, ok := f.get(ctx, "sc-versions/"+dev,
			base+"/browser-version-market-share/"+dev+"/worldwide/"+year, true); ok {
			s.versions[dev] = table(body)
			if s.families[dev] == nil {
				s.families[dev] = familyWeight(s.versions[dev])
			}
		}
		if body, ok := f.get(ctx, "sc-os/"+dev,
			base+"/os-market-share/"+dev+"/worldwide/"+year, true); ok {
			s.oses[dev] = table(body)
		}
	}
	if body, ok := f.get(ctx, "sc-devices",
		base+"/platform-market-share/desktop-mobile-tablet/worldwide/"+year, true); ok {
		// same snapshot table shape: Mobile/Desktop/Tablet rows
		for label, share := range table(body) {
			if dev := devKey(label); dev != "" {
				s.devices[dev] = share
			}
		}
	}
	return s
}
