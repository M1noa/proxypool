package uagen

import (
	"context"
	"encoding/json"
	"fmt"
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

// apple: latest ios/macos product versions from gdmf. entries carry
// posting dates; the newest posted version per os family wins.
func (f *fetcher) apple(ctx context.Context, v *versions) {
	var gdmf struct {
		PublicAssetSets map[string][]struct {
			ProductVersion string `json:"ProductVersion"`
			PostingDate    string `json:"PostingDate"`
		} `json:"PublicAssetSets"`
	}
	if !f.getJSON(ctx, "apple-gdmf", epApple, &gdmf) {
		return
	}
	newest := map[string]string{}
	newestDate := map[string]string{}
	for family, sets := range gdmf.PublicAssetSets {
		for _, s := range sets {
			if s.PostingDate > newestDate[family] {
				newestDate[family] = s.PostingDate
				newest[family] = s.ProductVersion
			}
		}
	}
	v.iosLatest = newest["iOS"]
	v.macosLatest = newest["macOS"]
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
