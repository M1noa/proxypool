package uagen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxRecords caps the pool. statcounter covers ~6 version rows per device;
// canonical composition plus wimb plus uap-core lands in the low hundreds.
const maxRecords = 500

// compose merges the three template layers (canonical, wimb, uap-core),
// weights them from statcounter, and returns records sorted by share desc.
func compose(v versions, wimb, seeds []wimbUA, s shares, now time.Time) []Record {
	c := &composer{v: v, s: s, now: now, seen: map[string]bool{}, cur: map[string]int{
		"chrome":  majorOf(v.chromeStable["Windows"]),
		"firefox": majorOf(v.firefox),
		"safari":  majorOf(v.safariVer),
		"edge":    majorOf(edgeNewest(v.edgeStable)),
		"opera":   majorOf(mdnVer(v, "opera")),
		"samsung": majorOf(mdnVer(v, "samsunginternet_android")),
	}}
	c.canonical()
	// ie has no version api and its last release was 2013; scraped ie/edge-
	// html samples are museum pieces, never pool entries.
	wimb = filterBrowser(wimb, "ie")
	seeds = filterBrowser(seeds, "ie")
	for _, w := range wimb {
		c.fromScrape(w, 0)
	}
	for _, w := range seeds {
		c.fromScrape(w, 0)
	}
	recs := c.recs
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Share != recs[j].Share {
			return recs[i].Share > recs[j].Share
		}
		return recs[i].UA < recs[j].UA
	})
	// normalize weights to sum 1
	var total float64
	for _, r := range recs {
		total += r.Share
	}
	if total > 0 {
		for i := range recs {
			recs[i].Share = round6(recs[i].Share / total)
		}
	}
	if len(recs) > maxRecords {
		recs = recs[:maxRecords]
	}
	stamp := now.UTC().Format(time.RFC3339)
	for i := range recs {
		recs[i].GeneratedAt = stamp
	}
	return recs
}

type composer struct {
	v    versions
	s    shares
	now  time.Time
	seen map[string]bool
	recs []Record
	// current majors per browser, for the freshness gate
	cur map[string]int
}

func (c *composer) add(r Record) {
	if r.UA == "" || c.seen[r.UA] {
		return
	}
	// freshness gate: drop records whose browser major is more than 2 behind
	// current, and known-ancient os tokens scraped from old guides (win 6.x,
	// android <= 10). without this a wimb guide that stops updating poisons
	// the pool with decade-old tokens that still get an epsilon share.
	if cur, ok := c.cur[r.Browser]; ok && cur > 0 {
		// version apis lag by days, so one major ahead is tolerated; more
		// than two behind is stale.
		if majorOf(r.BrowserVersion)-cur > 1 || cur-majorOf(r.BrowserVersion) > 2 {
			return
		}
	}
	if staleOS(r) {
		return
	}
	c.seen[r.UA] = true
	c.recs = append(c.recs, r)
}

// mdnVer returns the current version for an mdn browser key, "" if unknown.
func mdnVer(v versions, key string) string {
	if m, ok := v.mdn[key]; ok {
		return m.version
	}
	return ""
}

// filterBrowser drops scraped records of one browser family.
func filterBrowser(in []wimbUA, browser string) []wimbUA {
	out := in[:0]
	for _, w := range in {
		if w.browser != browser {
			out = append(out, w)
		}
	}
	return out
}

// staleOS rejects os versions no supported release still ships: windows nt
// 6.x (7/8/8.1), android 10 and below on scraped records (canonical composes
// android 17 directly).
func staleOS(r Record) bool {
	switch r.OS {
	case "windows":
		if strings.HasPrefix(r.OSVersion, "6.") {
			return true
		}
	case "android":
		if r.TemplateSource != "canonical" {
			n, err := strconv.Atoi(strings.SplitN(r.OSVersion, ".", 2)[0])
			if err != nil || n <= 10 {
				return true
			}
		}
	}
	return false
}

// weight multiplies device × version × os shares. the version term tries the
// exact statcounter label first, then the browser-family rollup, so records
// match a weight even when the 6-row snapshot names no exact version. labels
// that match nothing contribute a long-tail epsilon instead of zero, so rare
// but real agents sort last rather than vanishing.
func (c *composer) weight(device, browser, verLabel, osLabel string) float64 {
	const eps = 1e-4
	dev := c.s.devices[device]
	if dev == 0 {
		dev = eps
	}
	ver := eps
	for label, share := range c.s.versions[device] {
		if strings.Contains(label, verLabel) {
			ver = maxf(ver, share)
		}
	}
	if ver == eps {
		if fams := c.s.families[device]; fams != nil {
			for fam, share := range fams {
				if strings.Contains(strings.ToLower(browser), strings.ToLower(fam)) ||
					strings.Contains(strings.ToLower(fam), strings.ToLower(browser)) {
					ver = maxf(ver, share)
				}
			}
		}
	}
	os := eps
	for label, share := range c.s.oses[device] {
		if strings.Contains(strings.ToLower(label), strings.ToLower(osLabel)) {
			os = maxf(os, share)
		}
	}
	return dev * ver * os
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func round6(f float64) float64 {
	n, _ := strconv.ParseFloat(fmt.Sprintf("%.6f", f), 64)
	return n
}

// mdnDate returns the release date for an mdn browser key, "" if unknown.
func (c *composer) mdnDate(key string) string {
	if m, ok := c.v.mdn[key]; ok {
		return m.date
	}
	return ""
}

// canonical builds one UA per (browser, version, os) from format functions,
// the layer that always works even when every scrape fails.
func (c *composer) canonical() {
	v := c.v
	// chrome desktop: exact per-platform builds
	if full := v.chromeStable["Windows"]; full != "" {
		c.add(chromeWin(full, majorOf(full), c))
	}
	if full := v.chromeStable["Mac"]; full != "" {
		c.add(chromeMac(full, majorOf(full), c))
	}
	// chrome android
	if full := v.chromeStable["Android"]; full != "" {
		major := majorOf(full)
		for _, model := range []string{"", "SM-A205U", "SM-G960U", "Pixel 8", "HD1913"} {
			c.add(chromeAndroid(full, major, model, c))
		}
		// samsung internet rides the chromium build two majors back
		if m, ok := v.mdn["samsunginternet_android"]; ok {
			for _, model := range []string{"", "SM-S918B"} {
				c.add(samsung(m.version, full, model, c))
			}
		}
	}
	// edge desktop + android
	chromium := chromeBaseVer(v.chromeStable)
	if wv, ok := v.edgeStable["Windows"]; ok && wv != "" {
		c.add(edgeWin(wv, majorOf(wv), chromium, c))
	}
	if mv, ok := v.edgeStable["MacOS"]; ok && mv != "" {
		c.add(edgeMac(mv, majorOf(mv), chromium, c))
	}
	if av, ok := v.edgeStable["Android"]; ok && av != "" {
		c.add(edgeAndroid(av, majorOf(av), c))
	}
	// firefox desktop + android
	if v.firefox != "" {
		c.add(firefoxWin(v.firefox, c))
		c.add(firefoxMac(v.firefox, macOSVer(v), c))
		c.add(firefoxLinux(v.firefox, c))
	}
	if v.firefoxAndr != "" {
		c.add(firefoxAndroid(v.firefoxAndr, c))
	}
	// safari desktop + ios
	if v.safariVer != "" {
		c.add(safariMac(v.safariVer, macOSVer(v), v.safariWebkit, c))
		c.add(safariIPhone(v.safariVer, iosVer(v), c))
		c.add(safariIPad(v.safariVer, iosVer(v), c))
	}
	// opera desktop + android
	if m, ok := v.mdn["opera"]; ok {
		c.add(operaWin(m.version, chromium, c))
	}
	if m, ok := v.mdn["opera_android"]; ok {
		c.add(operaAndroid(m.version, chromium, c))
	}
	// ios webviews: crios + fxios ride the ios release
	if v.iosLatest != "" {
		if full := v.chromeStable["Android"]; full != "" {
			c.add(criOS(full, iosVer(v), c))
			c.add(criOSPad(full, iosVer(v), c))
		}
		if v.firefoxAndr != "" {
			c.add(fxiOS(v.firefoxAndr, iosVer(v), c))
		}
	}
}

func chromeBaseVer(m map[string]string) string {
	if s, ok := m["Windows"]; ok && s != "" {
		return s
	}
	for _, s := range m {
		if s != "" {
			return s
		}
	}
	return "152.0.0.0"
}

func macOSVer(v versions) string {
	if v.macosLatest != "" {
		return underscored(dotted(v.macosLatest))
	}
	return "15_7"
}

func iosVer(v versions) string {
	if v.iosLatest != "" {
		return underscored(dotted(v.iosLatest))
	}
	return "18_7"
}

// dotted pads a bare major to major.0: a just-released eol cycle like "27"
// has no patch yet, but real agents always carry at least minor.
func dotted(s string) string {
	if !strings.Contains(s, ".") {
		return s + ".0"
	}
	return s
}

func underscored(s string) string {
	return strings.ReplaceAll(s, ".", "_")
}

// --- canonical format functions. each mirrors the current ground-truth
// grammar per family (verified against wimb 2026-09-14). ---

func chromeWin(full string, major int, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", full)
	return Record{UA: ua, Browser: "chrome", BrowserVersion: full,
		OS: "windows", OSVersion: "10", Device: "desktop",
		Share:       c.weight("desktop", "chrome", "Chrome "+strconv.Itoa(major), "windows"),
		VersionDate: c.mdnDate("chrome"), TemplateSource: "canonical"}
}

func chromeMac(full string, major int, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", full)
	return Record{UA: ua, Browser: "chrome", BrowserVersion: full,
		OS: "macos", OSVersion: "10.15.7", Device: "desktop",
		Share:       c.weight("desktop", "chrome", "Chrome "+strconv.Itoa(major), "mac"),
		VersionDate: c.mdnDate("chrome"), TemplateSource: "canonical"}
}

func chromeAndroid(full string, major int, model string, c *composer) Record {
	tok := "Linux; Android 17"
	if model != "" {
		tok += "; " + model
	}
	ua := fmt.Sprintf("Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36", tok, full)
	return Record{UA: ua, Browser: "chrome", BrowserVersion: full,
		OS: "android", OSVersion: "17", Device: "mobile",
		Share:       c.weight("mobile", "chrome", "Chrome for Android", "android"),
		VersionDate: c.mdnDate("chrome_android"), TemplateSource: "canonical"}
}

func samsung(ver, chromium, model string, c *composer) Record {
	tok := "Linux; Android 17"
	if model != "" {
		tok += "; " + model
	}
	ua := fmt.Sprintf("Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/%s Chrome/%s Mobile Safari/537.36", tok, ver, chromium)
	return Record{UA: ua, Browser: "samsung", BrowserVersion: ver,
		OS: "android", OSVersion: "17", Device: "mobile",
		Share:       c.weight("mobile", "samsung", "Samsung", "android"),
		VersionDate: c.mdnDate("samsunginternet_android"), TemplateSource: "canonical"}
}

func edgeWin(full string, major int, chromium string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36 Edg/%s", chromiumBase(major), full)
	return Record{UA: ua, Browser: "edge", BrowserVersion: full,
		OS: "windows", OSVersion: "10", Device: "desktop",
		Share:       c.weight("desktop", "edge", "Edge "+strconv.Itoa(major), "windows"),
		VersionDate: c.mdnDate("edge"), TemplateSource: "canonical"}
}

func edgeMac(full string, major int, chromium string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36 Edg/%s", chromiumBase(major), full)
	return Record{UA: ua, Browser: "edge", BrowserVersion: full,
		OS: "macos", OSVersion: "10.15.7", Device: "desktop",
		Share:       c.weight("desktop", "edge", "Edge "+strconv.Itoa(major), "mac"),
		VersionDate: c.mdnDate("edge"), TemplateSource: "canonical"}
}

func edgeAndroid(full string, major int, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Linux; Android 17; HD1913) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.7977.84 Mobile Safari/537.36 EdgA/%s", major, full)
	return Record{UA: ua, Browser: "edge", BrowserVersion: full,
		OS: "android", OSVersion: "17", Device: "mobile",
		Share:       c.weight("mobile", "edge", "Edge", "android"),
		VersionDate: c.mdnDate("edge"), TemplateSource: "canonical"}
}

// chromiumBase renders the Chrome/ token edge carries: same major, desktop
// .0.0.0 style (matches ground truth).
func chromiumBase(major int) string {
	return strconv.Itoa(major) + ".0.0.0"
}

func firefoxWin(full string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%s) Gecko/20100101 Firefox/%s", shortVer(full), full)
	return Record{UA: ua, Browser: "firefox", BrowserVersion: full,
		OS: "windows", OSVersion: "10", Device: "desktop",
		Share:       c.weight("desktop", "firefox", "Firefox", "windows"),
		VersionDate: c.mdnDate("firefox"), TemplateSource: "canonical"}
}

func firefoxMac(full, macVer string, c *composer) Record {
	dotted := strings.ReplaceAll(macVer, "_", ".")
	ua := fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X %s; rv:%s) Gecko/20100101 Firefox/%s", dotted, shortVer(full), full)
	return Record{UA: ua, Browser: "firefox", BrowserVersion: full,
		OS: "macos", OSVersion: dotted, Device: "desktop",
		Share:       c.weight("desktop", "firefox", "Firefox", "mac"),
		VersionDate: c.mdnDate("firefox"), TemplateSource: "canonical"}
}

func firefoxLinux(full string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64; rv:%s) Gecko/20100101 Firefox/%s", shortVer(full), full)
	return Record{UA: ua, Browser: "firefox", BrowserVersion: full,
		OS: "linux", OSVersion: "", Device: "desktop",
		Share:       c.weight("desktop", "firefox", "Firefox", "linux"),
		VersionDate: c.mdnDate("firefox"), TemplateSource: "canonical"}
}

func firefoxAndroid(full string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Android 17; Mobile; rv:%s) Gecko/%s Firefox/%s", shortVer(full), full, full)
	return Record{UA: ua, Browser: "firefox", BrowserVersion: full,
		OS: "android", OSVersion: "17", Device: "mobile",
		Share:       c.weight("mobile", "firefox", "Firefox", "android"),
		VersionDate: c.mdnDate("firefox_android"), TemplateSource: "canonical"}
}

func safariMac(ver, macVer, webkit string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X %s) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%s Safari/%s", macVer, ver, webkit)
	return Record{UA: ua, Browser: "safari", BrowserVersion: ver,
		OS: "macos", OSVersion: strings.ReplaceAll(macVer, "_", "."), Device: "desktop",
		Share:       c.weight("desktop", "safari", "Safari", "mac"),
		VersionDate: c.mdnDate("safari"), TemplateSource: "canonical"}
}

func safariIPhone(ver, iosVer string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%s Mobile/15E148 Safari/604.1", iosVer, ver)
	return Record{UA: ua, Browser: "safari", BrowserVersion: ver,
		OS: "ios", OSVersion: strings.ReplaceAll(iosVer, "_", "."), Device: "mobile",
		Share:       c.weight("mobile", "safari", "Safari iPhone", "ios"),
		VersionDate: c.mdnDate("safari_ios"), TemplateSource: "canonical"}
}

func safariIPad(ver, iosVer string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (iPad; CPU OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%s Mobile/15E148 Safari/604.1", iosVer, ver)
	return Record{UA: ua, Browser: "safari", BrowserVersion: ver,
		OS: "ios", OSVersion: strings.ReplaceAll(iosVer, "_", "."), Device: "tablet",
		Share:       c.weight("tablet", "safari", "Safari iPad", "ios"),
		VersionDate: c.mdnDate("safari_ios"), TemplateSource: "canonical"}
}

func operaWin(ver, chromium string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36 OPR/%s", chromiumBase(majorOf(chromium)), ver)
	return Record{UA: ua, Browser: "opera", BrowserVersion: ver,
		OS: "windows", OSVersion: "10", Device: "desktop",
		Share:       c.weight("desktop", "opera", "Opera", "windows"),
		VersionDate: c.mdnDate("opera"), TemplateSource: "canonical"}
}

func operaAndroid(ver, chromium string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (Linux; Android 17; HD1913) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Mobile Safari/537.36 OPR/%s", chromium, ver)
	return Record{UA: ua, Browser: "opera", BrowserVersion: ver,
		OS: "android", OSVersion: "17", Device: "mobile",
		Share:       c.weight("mobile", "opera", "Opera", "android"),
		VersionDate: c.mdnDate("opera_android"), TemplateSource: "canonical"}
}

func criOS(chromium, ios string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/%s Mobile/15E148 Safari/604.1", ios, chromium)
	return Record{UA: ua, Browser: "chrome", BrowserVersion: chromium,
		OS: "ios", OSVersion: strings.ReplaceAll(ios, "_", "."), Device: "mobile",
		Share:       c.weight("mobile", "chrome", "Chrome for iPhone", "ios"),
		VersionDate: c.mdnDate("chrome_android"), TemplateSource: "canonical"}
}

func criOSPad(chromium, ios string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (iPad; CPU OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/%s Mobile/15E148 Safari/604.1", ios, chromium)
	return Record{UA: ua, Browser: "chrome", BrowserVersion: chromium,
		OS: "ios", OSVersion: strings.ReplaceAll(ios, "_", "."), Device: "tablet",
		Share:       c.weight("tablet", "chrome", "Chrome iPad", "ios"),
		VersionDate: c.mdnDate("chrome_android"), TemplateSource: "canonical"}
}

func fxiOS(full, ios string, c *composer) Record {
	ua := fmt.Sprintf("Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/%s Mobile/15E148 Safari/605.1.15", ios, shortVer(full))
	return Record{UA: ua, Browser: "firefox", BrowserVersion: full,
		OS: "ios", OSVersion: strings.ReplaceAll(ios, "_", "."), Device: "mobile",
		Share:       c.weight("mobile", "firefox", "Firefox", "ios"),
		VersionDate: c.mdnDate("firefox_android"), TemplateSource: "canonical"}
}

// shortVer trims 155.0.1 to 155.0 for the rv: token.
func shortVer(full string) string {
	parts := strings.SplitN(full, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return full
}

// fromScrape turns a scraped agent into a record, weighting it against the
// statcounter labels for its device.
func (c *composer) fromScrape(w wimbUA, _ int) {
	verLabel := scrapeLabel(w)
	osLabel := w.os
	if w.browser == "safari" && w.device == "mobile" {
		osLabel = "iphone"
	}
	date := ""
	switch w.browser {
	case "chrome":
		date = c.mdnDate("chrome")
	case "firefox":
		date = c.mdnDate("firefox")
	case "safari":
		date = c.mdnDate("safari")
	case "edge":
		date = c.mdnDate("edge")
	case "opera":
		date = c.mdnDate("opera")
	}
	c.add(Record{UA: w.ua, Browser: w.browser, BrowserVersion: w.version,
		OS: w.os, OSVersion: w.osVer, Device: w.device,
		Share:       c.weight(w.device, w.browser, verLabel, osLabel),
		VersionDate: date, TemplateSource: w.source})
}

// scrapeLabel maps a scraped agent to the statcounter version label most
// likely to carry its share (major match).
func scrapeLabel(w wimbUA) string {
	major := strings.SplitN(w.version, ".", 2)[0]
	switch w.browser {
	case "chrome":
		if w.os == "ios" {
			return "Chrome for iPhone"
		}
		if w.device == "mobile" {
			return "Chrome for Android"
		}
		return "Chrome " + major + ".0"
	case "safari":
		if w.device == "tablet" {
			return "Safari iPad"
		}
		if w.device == "mobile" {
			return "Safari iPhone"
		}
		return "Safari"
	case "edge":
		return "Edge " + major
	case "firefox":
		return "Firefox " + major
	case "opera":
		return "Opera " + major
	case "samsung":
		return "Samsung"
	}
	return w.browser + " " + major
}
