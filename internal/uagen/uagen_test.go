package uagen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// fixture server answering every input endpoint with canned bodies.
func fixture(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chromiumdash", func(w http.ResponseWriter, r *http.Request) {
		plat := r.URL.Query().Get("platform")
		w.Write([]byte(`[{"version":"154.0.8037.21","platform":"` + plat + `"}]`))
	})
	mux.HandleFunc("/cft.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"channels":{"Stable":{"version":"153.0.8010.36"},"Beta":{"version":"154.0.8037.0"},"Dev":{"version":"155.0.8048.0"}}}`))
	})
	mux.HandleFunc("/firefox.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"LATEST_FIREFOX_VERSION":"155.0.1","LATEST_FIREFOX_DEVEL_VERSION":"156.0b3","FIREFOX_ESR":"140.15.0esr"}`))
	})
	mux.HandleFunc("/mobile.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"155.0.1"}`))
	})
	mux.HandleFunc("/edge", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"Product":"Stable","Releases":[{"Platform":"Windows","ProductVersion":"153.0.4234.32"}]}]`))
	})
	mux.HandleFunc("/eol/", func(w http.ResponseWriter, r *http.Request) {
		body := `[{"cycle":"27","latest":"27","eol":false}]`
		if strings.Contains(r.URL.Path, "macos") {
			body = `[{"cycle":"27","latest":"27","eol":false},{"cycle":"15","latest":"15.8","eol":false}]`
		}
		w.Write([]byte(body))
	})
	mux.HandleFunc("/mdn/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/mdn/"), ".json")
		ver := "154"
		if strings.Contains(key, "safari") {
			ver = "27"
		}
		if strings.Contains(key, "samsung") {
			ver = "30.0"
		}
		if strings.Contains(key, "opera") {
			ver = "135"
		}
		w.Write([]byte(`{"browsers":{"b":{"releases":{"` + ver + `":{"release_date":"2026-09-01","status":"current","engine_version":"150"}}}}}`))
	})
	mux.HandleFunc("/sc/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		rows := `<th>Chrome 154.0</th><td><span class="count">9.29</span></td>`
		switch {
		case strings.Contains(p, "platform"):
			rows = `<th>Mobile</th><td><span class="count">49.36</span></td>` +
				`<th>Desktop</th><td><span class="count">49.11</span></td>` +
				`<th>Tablet</th><td><span class="count">1.54</span></td>`
		case strings.Contains(p, "os-market"):
			rows = `<th>Windows</th><td><span class="count">62.67</span></td>` +
				`<th>Android</th><td><span class="count">40.00</span></td>`
		case strings.Contains(p, "browser-market"):
			rows = `<th>Chrome</th><td><span class="count">73.28</span></td>` +
				`<th>Edge</th><td><span class="count">10.46</span></td>` +
				`<th>Firefox</th><td><span class="count">5.31</span></td>`
		case strings.Contains(p, "mobile"):
			rows = `<th>Chrome for Android</th><td><span class="count">60.29</span></td>`
		}
		w.Write([]byte(`<table class="stats-snapshot"><tbody><tr>` + rows + `</tr></tbody></table>`))
	})
	mux.HandleFunc("/wimb/", func(w http.ResponseWriter, r *http.Request) {
		// current chrome plus three stale decoys: old chrome, win7 ie, old opera
		w.Write([]byte(`<span class="code">Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36</span>` +
			`<span class="code">Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36</span>` +
			`<span class="code">Mozilla/5.0 (compatible; MSIE 9.0; Windows NT 6.1; Trident/5.0)</span>` +
			`<span class="code">Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 OPR/76.2.4027.73374</span>`))
	})
	mux.HandleFunc("/corpus.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test_cases:\n  - user_agent_string: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36'\n    family: 'Chrome'\n    major: '154'\n"))
	})
	s := serve(t, mux.ServeHTTP)
	epChromiumDash = s.URL + "/chromiumdash"
	epChromeCFT = s.URL + "/cft.json"
	epFirefox = s.URL + "/firefox.json"
	epFirefoxMob = s.URL + "/mobile.json"
	epEdge = s.URL + "/edge"
	epEOL = s.URL + "/eol/"
	epMDN = s.URL + "/mdn/"
	epStatcounter = s.URL + "/sc"
	epWimb = s.URL + "/wimb/"
	epUACore = s.URL + "/corpus.yaml"
}

func TestGenerateFixture(t *testing.T) {
	fixture(t)
	recs, warnings := Generate(context.Background(), time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(recs) == 0 {
		t.Fatal("no records")
	}
	seen := map[string]bool{}
	var total float64
	for _, r := range recs {
		if r.UA == "" || seen[r.UA] {
			t.Errorf("empty or dup ua: %q", r.UA)
		}
		seen[r.UA] = true
		if r.Browser == "" || r.OS == "" || r.Device == "" || r.GeneratedAt == "" {
			t.Errorf("missing fields: %+v", r)
		}
		total += r.Share
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("share sum = %f, want 1", total)
	}
	// sorted desc
	for i := 1; i < len(recs); i++ {
		if recs[i].Share > recs[i-1].Share {
			t.Fatal("not sorted by share desc")
		}
	}
}

func TestVersionsFixture(t *testing.T) {
	fixture(t)
	f := &fetcher{now: time.Now()}
	v := f.versions(context.Background())
	if len(f.warnings) > 0 {
		t.Fatalf("warnings: %v", f.warnings)
	}
	if v.chromeStable["Windows"] != "154.0.8037.21" {
		t.Errorf("chrome windows = %q", v.chromeStable["Windows"])
	}
	if v.firefox != "155.0.1" || v.firefoxESR != "140.15.0" {
		t.Errorf("firefox = %q esr = %q", v.firefox, v.firefoxESR)
	}
	if v.iosLatest != "27" || v.macosLatest != "27" {
		t.Errorf("ios = %q macos = %q", v.iosLatest, v.macosLatest)
	}
	if v.safariVer != "27" {
		t.Errorf("safari = %q", v.safariVer)
	}
}

func TestStaleGate(t *testing.T) {
	fixture(t)
	recs, warnings := Generate(context.Background(), time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC))
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	for _, r := range recs {
		// stale decoys from the wimb fixture must not survive: chrome 120
		// (>2 behind 154), win 6.1 ie, opera 76 (>2 behind 135)
		if r.Browser == "ie" {
			t.Errorf("ie record survived: %q", r.UA)
		}
		if r.Browser == "chrome" && majorOf(r.BrowserVersion) < 152 {
			t.Errorf("stale chrome survived: %q", r.UA)
		}
		if r.Browser == "opera" && majorOf(r.BrowserVersion) < 133 {
			t.Errorf("stale opera survived: %q", r.UA)
		}
		if strings.Contains(r.UA, "Windows NT 6.") {
			t.Errorf("win6 record survived: %q", r.UA)
		}
	}
	// canonical chrome matches the family weight, not epsilon
	for _, r := range recs {
		if r.TemplateSource == "canonical" && r.Browser == "chrome" && r.OS == "windows" {
			if r.Share < 0.01 {
				t.Errorf("canonical chrome share = %f, want family weight", r.Share)
			}
		}
	}
}

func TestParseWimbUA(t *testing.T) {
	cases := []struct {
		ua                                  string
		browser, version, os, device, osVer string
	}{
		{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36",
			"chrome", "154.0.0.0", "windows", "desktop", "10.0",
		},
		{
			"Mozilla/5.0 (iPhone; CPU iPhone OS 18_7_8 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0 Mobile/15E148 Safari/604.1",
			"safari", "27.0", "ios", "mobile", "18.7.8",
		},
		{
			"Mozilla/5.0 (Linux; Android 17; SM-A205U) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.8037.21 Mobile Safari/537.36",
			"chrome", "154.0.8037.21", "android", "mobile", "17",
		},
		{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36 Edg/153.0.4234.32",
			"edge", "153.0.4234.32", "windows", "desktop", "10.0",
		},
	}
	for _, tc := range cases {
		w, ok := parseWimbUA(tc.ua, "test")
		if !ok {
			t.Errorf("rejected %q", tc.ua[:60])
			continue
		}
		if w.browser != tc.browser || w.version != tc.version || w.os != tc.os ||
			w.device != tc.device || w.osVer != tc.osVer {
			t.Errorf("got %+v, want %s/%s/%s/%s/%s", w,
				tc.browser, tc.version, tc.os, tc.device, tc.osVer)
		}
	}
}
