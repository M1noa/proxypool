package uagen

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/M1noa/proxypool/internal/httpx"
)

// versions holds every live version input compose() needs. empty fields mean
// that input failed and its families are skipped.
type versions struct {
	chromeStable map[string]string // platform -> full build, chromiumdash
	chromeBeta   string            // cft beta full build
	chromeDev    string            // cft dev full build
	firefox      string
	firefoxBeta  string
	firefoxESR   string
	firefoxAndr  string
	edgeStable   map[string]string // platform -> full build
	iosLatest    string            // e.g. 18.7.8
	macosLatest  string            // e.g. 15.7.9
	safariVer    string            // current desktop+ios safari, e.g. 27
	safariWebkit string            // e.g. 625.1.29
	mdn          map[string]mdnRel // mdn browser key -> current release
}

// mdnRel is the current release row of one mdn browsers/*.json file.
type mdnRel struct {
	version   string
	date      string
	engineVer string
}

type fetcher struct {
	now      time.Time
	warnings []string
	c        *httpx.Client
}

func (f *fetcher) client() *httpx.Client {
	if f.c == nil {
		f.c = httpx.New(nil, fetchTimeout)
		defer f.c.CloseIdle()
	}
	return f.c
}

func (f *fetcher) warn(input string, err error) {
	f.warnings = append(f.warnings, fmt.Sprintf("%s: %v", input, err))
}

// get fetches url and returns its body. statcounter and wimb want a browser
// ua; the version apis get the default one.
func (f *fetcher) get(ctx context.Context, name, url string, browserUA bool) (string, bool) {
	req := httpx.Req{URL: url, Timeout: fetchTimeout}
	if browserUA {
		req.Headers = map[string]string{"User-Agent": httpx.BrowserUA}
	}
	body, err := f.client().Do(ctx, req)
	if err != nil {
		f.warn(name, err)
		return "", false
	}
	return body, true
}

func (f *fetcher) getJSON(ctx context.Context, name, url string, v any) bool {
	body, ok := f.get(ctx, name, url, false)
	if !ok {
		return false
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		f.warn(name, err)
		return false
	}
	return true
}

// versions fetches every version input. each failure warns and leaves its
// fields empty; compose skips families with nothing to build from.
func (f *fetcher) versions(ctx context.Context) versions {
	var v versions
	f.chrome(ctx, &v)
	f.firefox(ctx, &v)
	f.edge(ctx, &v)
	f.apple(ctx, &v)
	f.mdn(ctx, &v)
	return v
}

// chrome: exact builds per platform from chromiumdash, channels from
// chrome-for-testing.
func (f *fetcher) chrome(ctx context.Context, v *versions) {
	type rel struct {
		Version  string `json:"version"`
		Platform string `json:"platform"`
	}
	v.chromeStable = map[string]string{}
	for _, plat := range []string{"Windows", "Mac", "Android"} {
		var rels []rel
		url := epChromiumDash + "?channel=Stable&platform=" + plat + "&num=3"
		if !f.getJSON(ctx, "chromiumdash/"+plat, url, &rels) {
			continue
		}
		if len(rels) > 0 {
			v.chromeStable[plat] = rels[0].Version
		}
	}
	var cft struct {
		Channels map[string]struct {
			Version string `json:"version"`
		} `json:"channels"`
	}
	if f.getJSON(ctx, "chrome-for-testing",
		epChromeCFT, &cft) {
		v.chromeBeta = cft.Channels["Beta"].Version
		v.chromeDev = cft.Channels["Dev"].Version
		// backfill platforms chromiumdash missed with the stable channel
		if sv := cft.Channels["Stable"].Version; sv != "" {
			for _, plat := range []string{"Windows", "Mac", "Android"} {
				if v.chromeStable[plat] == "" {
					v.chromeStable[plat] = sv
				}
			}
		}
	}
}

// firefox: desktop release/beta/esr plus android from product-details.
func (f *fetcher) firefox(ctx context.Context, v *versions) {
	var det map[string]string
	if f.getJSON(ctx, "firefox-versions",
		epFirefox, &det) {
		v.firefox = strings.TrimSuffix(det["LATEST_FIREFOX_VERSION"], "")
		v.firefoxBeta = det["LATEST_FIREFOX_DEVEL_VERSION"]
		v.firefoxESR = strings.TrimSuffix(det["FIREFOX_ESR"], "esr")
	}
	var mob map[string]string
	if f.getJSON(ctx, "firefox-mobile",
		epFirefoxMob, &mob) {
		v.firefoxAndr = mob["version"]
	}
}

// edge: stable build per platform from the update api.
func (f *fetcher) edge(ctx context.Context, v *versions) {
	var prods []struct {
		Product  string `json:"Product"`
		Releases []struct {
			Platform       string `json:"Platform"`
			ProductVersion string `json:"ProductVersion"`
		} `json:"Releases"`
	}
	if !f.getJSON(ctx, "edge", epEdge, &prods) {
		return
	}
	v.edgeStable = map[string]string{}
	for _, p := range prods {
		if p.Product != "Stable" {
			continue
		}
		for _, r := range p.Releases {
			if r.ProductVersion == "" {
				continue
			}
			// first release per platform wins; api lists newest first
			if _, ok := v.edgeStable[r.Platform]; !ok {
				v.edgeStable[r.Platform] = r.ProductVersion
			}
		}
	}
}

// apple: latest supported ios/macos from endoflife.date. gdmf's cert chain
// fails verification on the ci runners (x509 unknown authority), so every
// run warned and skipped apple input. eol serves plain json per product:
// [{cycle, latest, eol}]; newest non-eol cycle with a latest wins.
func (f *fetcher) apple(ctx context.Context, v *versions) {
	type cycle struct {
		Cycle  string `json:"cycle"`
		Latest string `json:"latest"`
		EOL    any    `json:"eol"`
	}
	pick := func(name string) string {
		var cycles []cycle
		if !f.getJSON(ctx, "eol/"+name, epEOL+name+".json", &cycles) {
			return ""
		}
		best := ""
		for _, c := range cycles {
			// eol is false while supported, a date string once dropped
			if c.Latest == "" || c.EOL != false {
				continue
			}
			if best == "" || compareVer(c.Latest, best) > 0 {
				best = c.Latest
			}
		}
		return best
	}
	v.iosLatest = pick("ios")
	v.macosLatest = pick("macos")
}

// compareVer orders dotted numeric versions: 27 > 26.7, 15.8 > 15.7.
func compareVer(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, _ := strconv.Atoi(as[i])
		bi, _ := strconv.Atoi(bs[i])
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	switch {
	case len(as) > len(bs):
		return 1
	case len(as) < len(bs):
		return -1
	}
	return 0
}

// mdn: current release per browser file — version, date, engine mapping.
// safari's row is also where safariVer/webkit come from.
func (f *fetcher) mdn(ctx context.Context, v *versions) {
	v.mdn = map[string]mdnRel{}
	for _, key := range []string{"chrome", "chrome_android", "edge", "firefox",
		"firefox_android", "safari", "safari_ios", "samsunginternet_android",
		"opera", "opera_android"} {
		var doc struct {
			Browsers map[string]struct {
				Releases map[string]struct {
					Date      string `json:"release_date"`
					Status    string `json:"status"`
					EngineVer string `json:"engine_version"`
				} `json:"releases"`
			} `json:"browsers"`
		}
		url := epMDN + key + ".json"
		if !f.getJSON(ctx, "mdn/"+key, url, &doc) {
			continue
		}
		for _, b := range doc.Browsers {
			best, bestDate := "", ""
			for ver, r := range b.Releases {
				if r.Status != "current" || r.Date <= bestDate {
					continue
				}
				best, bestDate = ver, r.Date
			}
			if best != "" {
				v.mdn[key] = mdnRel{version: best, date: bestDate,
					engineVer: b.Releases[best].EngineVer}
			}
		}
	}
	if s, ok := v.mdn["safari"]; ok {
		v.safariVer, v.safariWebkit = s.version, s.engineVer
	}
}
