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
type shares struct {
	versions map[string]map[string]float64 // device -> label -> share
	oses     map[string]map[string]float64 // device -> label -> share
	devices  map[string]float64            // desktop/mobile/tablet
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

// shares fetches the statcounter tables: browser versions and oses per
// device, plus the device split. year urls serve the latest full month.
func (f *fetcher) shares(ctx context.Context) shares {
	s := shares{
		versions: map[string]map[string]float64{},
		oses:     map[string]map[string]float64{},
		devices:  map[string]float64{},
	}
	year := fmt.Sprintf("%d", f.now.Year())
	base := epStatcounter
	for _, dev := range []string{"desktop", "mobile", "tablet"} {
		if body, ok := f.get(ctx, "sc-versions/"+dev,
			base+"/browser-version-market-share/"+dev+"/worldwide/"+year, true); ok {
			s.versions[dev] = table(body)
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
			if dev, ok := map[string]string{
				"mobile": "mobile", "desktop": "desktop", "tablet": "tablet",
			}[strings.ToLower(label)]; ok {
				s.devices[dev] = share
			}
		}
	}
	return s
}
