package uagen

import (
	"context"
	"html"
	"regexp"
	"strings"
)

// wimbUA is one ground-truth agent scraped from a whatismybrowser guide,
// parsed into its parts.
type wimbUA struct {
	ua      string
	browser string // chrome, firefox, safari, edge, opera, ie
	version string // full version token
	os      string // windows, macos, linux, android, ios
	osVer   string // raw os token, e.g. 10_15_7, 18_7_8, 17
	device  string // desktop or mobile
	source  string // guide slug
}

var (
	reWimbCode = regexp.MustCompile(`<span class="code">(.*?)</span>`)
	// version tokens per family
	reChromeVer = regexp.MustCompile(`Chrome/([\d.]+)`)
	reCriOSVer  = regexp.MustCompile(`CriOS/([\d.]+)`)
	reFfVer     = regexp.MustCompile(`Firefox/([\d.]+)`)
	reFxiOSVer  = regexp.MustCompile(`FxiOS/([\d.]+)`)
	reSafariVer = regexp.MustCompile(`Version/([\d.]+)`)
	reEdgVer    = regexp.MustCompile(`Edg/([\d.]+)`)
	reEdgAVer   = regexp.MustCompile(`EdgA/([\d.]+)`)
	reOprVer    = regexp.MustCompile(`OPR/([\d.]+)`)
	reTrident   = regexp.MustCompile(`Trident/[\d.]+;\s*rv:([\d.]+)`)
	// os tokens
	reWinNT   = regexp.MustCompile(`Windows NT ([\d.]+)`)
	reMacOS   = regexp.MustCompile(`Mac OS X (\d+[_.]\d+(?:[_.]\d+)?)`)
	reAndroid = regexp.MustCompile(`Android ([\d.]+)`)
	reIOS     = regexp.MustCompile(`iPhone OS (\d+[_.]\d+(?:[_.]\d+)?)`)
	reCPUOS   = regexp.MustCompile(`CPU OS (\d+[_.]\d+(?:[_.]\d+)?)`)
	reLinux   = regexp.MustCompile(`Linux| X11;`)
)

// wimbGuides are the whatismybrowser latest-ua guide slugs worth scraping.
// browser guides give family templates; os guides give device/os variety.
var wimbGuides = []string{
	"chrome", "firefox", "safari", "edge", "opera", "vivaldi",
	"yandex-browser", "internet-explorer",
	"windows", "macos", "android", "ios", "chrome-os",
}

// wimb scrapes the guides and parses every sample agent. guides that fail
// are warned and skipped; the canonical composers cover the same families.
func (f *fetcher) wimb(ctx context.Context) []wimbUA {
	var out []wimbUA
	for _, slug := range wimbGuides {
		body, ok := f.get(ctx, "wimb/"+slug,
			epWimb+slug, true)
		if !ok {
			continue
		}
		for _, m := range reWimbCode.FindAllStringSubmatch(body, -1) {
			ua := html.UnescapeString(m[1])
			if !strings.HasPrefix(ua, "Mozilla/5.0") {
				continue
			}
			if w, ok := parseWimbUA(ua, slug); ok {
				out = append(out, w)
			}
		}
	}
	return out
}

// parseWimbUA splits a sample agent into family/version/os parts.
func parseWimbUA(ua, slug string) (wimbUA, bool) {
	w := wimbUA{ua: ua, source: "whatismybrowser/" + slug}
	switch {
	case strings.Contains(ua, "CriOS/"):
		w.browser, w.version = "chrome", firstGroup(reCriOSVer, ua)
	case strings.Contains(ua, "EdgA/"):
		w.browser, w.version = "edge", firstGroup(reEdgAVer, ua)
	case strings.Contains(ua, "Edg/"):
		w.browser, w.version = "edge", firstGroup(reEdgVer, ua)
	case strings.Contains(ua, "Chrome/") && !strings.Contains(ua, "OPR/") &&
		!strings.Contains(ua, "Vivaldi/") && !strings.Contains(ua, "YaBrowser/"):
		w.browser, w.version = "chrome", firstGroup(reChromeVer, ua)
	case strings.Contains(ua, "FxiOS/"):
		w.browser, w.version = "firefox", firstGroup(reFxiOSVer, ua)
	case strings.Contains(ua, "Firefox/"):
		w.browser, w.version = "firefox", firstGroup(reFfVer, ua)
	case strings.Contains(ua, "OPR/"):
		w.browser, w.version = "opera", firstGroup(reOprVer, ua)
	case strings.Contains(ua, "Vivaldi/"):
		w.browser = "vivaldi"
		w.version = firstGroup(regexp.MustCompile(`Vivaldi/([\d.]+)`), ua)
	case strings.Contains(ua, "YaBrowser/"):
		w.browser = "yandex"
		w.version = firstGroup(regexp.MustCompile(`YaBrowser/([\d.]+)`), ua)
	case strings.Contains(ua, "Version/"):
		w.browser, w.version = "safari", firstGroup(reSafariVer, ua)
	case strings.Contains(ua, "Trident/"):
		w.browser, w.version = "ie", firstGroup(reTrident, ua)
	default:
		return w, false
	}
	switch {
	case strings.Contains(ua, "Windows NT"):
		w.os, w.osVer, w.device = "windows", firstGroup(reWinNT, ua), "desktop"
	case strings.Contains(ua, "Macintosh"):
		w.os, w.osVer, w.device = "macos", underscore(firstGroup(reMacOS, ua)), "desktop"
	case strings.Contains(ua, "Android"):
		w.os, w.osVer, w.device = "android", firstGroup(reAndroid, ua), "mobile"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		w.os, w.device = "ios", "mobile"
		w.osVer = underscore(firstOf(firstGroup(reIOS, ua), firstGroup(reCPUOS, ua)))
	case strings.Contains(ua, "Linux") || strings.Contains(ua, "X11"):
		w.os, w.device = "linux", "desktop"
		w.osVer = ""
	case strings.Contains(ua, "CrOS"):
		w.os, w.device = "chromeos", "desktop"
		w.osVer = firstGroup(regexp.MustCompile(`CrOS \S+ ([\d.]+)`), ua)
	default:
		return w, false
	}
	if w.version == "" {
		return w, false
	}
	return w, true
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// underscore normalizes 10_15_7 to dotted form for the os_version field.
func underscore(s string) string {
	return strings.ReplaceAll(s, "_", ".")
}
