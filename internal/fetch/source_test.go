package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
)

// mustSource builds a source from the same json a sources.jsonc entry would
// carry, so the tests exercise the real config decoding rather than a
// hand-assembled struct.
func mustSource(t *testing.T, tmpl string, args ...any) *config.Source {
	t.Helper()
	var src config.Source
	body := fmt.Sprintf(tmpl, args...)
	if err := json.Unmarshal([]byte(body), &src); err != nil {
		t.Fatalf("source json: %v\n%s", err, body)
	}
	return &src
}

func run(t *testing.T, src *config.Source) ([]*extract.Record, []string) {
	t.Helper()
	f := &Fetcher{}
	return f.Source(context.Background(), src, &State{})
}

// ips renders the records as "ip:port" so a page walk is readable in a failure.
func ips(recs []*extract.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = fmt.Sprintf("%s:%d", r.IP, r.Port)
	}
	return out
}

// pageServer answers each request with one proxy row numbered after the page
// query, so the caller can assert exactly which pages were walked. it stops
// producing rows past stopAfter, which is what ends a page walk normally.
func pageServer(t *testing.T, stopAfter int, meta string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		p := r.URL.Query().Get("p")
		items := fmt.Sprintf(`[{"ip":"1.2.3.%s","port":8080}]`, p)
		if n := hits.Load(); int(n) > stopAfter {
			items = `[]`
		}
		fmt.Fprintf(w, `{%s"items":%s}`, meta, items)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const jsonExtract = `"format":"json","extract":{"root":"items","ip":"ip","port":"port"}`

func TestPaginationTotalPathRaisesCap(t *testing.T) {
	// total_path can push the walk past the configured max_pages: 250 rows at
	// 100 per page is 3 pages even though the source asked for 1.
	srv, hits := pageServer(t, 9, `"total":250,`)
	src := mustSource(t, `{"name":"t",%s,"url":%q,
		"pagination":{"max_pages":1,"total_path":"total","limit":100}}`,
		jsonExtract, srv.URL+"/?p={page}")

	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got, want := hits.Load(), int64(3); got != want {
		t.Fatalf("pages fetched = %d, want %d", got, want)
	}
	if got := strings.Join(ips(recs), " "); got != "1.2.3.1:8080 1.2.3.2:8080 1.2.3.3:8080" {
		t.Fatalf("records = %s", got)
	}
}

func TestPaginationPagesPathOnlyLowersCap(t *testing.T) {
	srv, hits := pageServer(t, 9, `"page_count":2,`)
	src := mustSource(t, `{"name":"t",%s,"url":%q,
		"pagination":{"max_pages":5,"pages_path":"page_count"}}`,
		jsonExtract, srv.URL+"/?p={page}")
	if _, errs := run(t, src); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("pages fetched = %d, want 2", got)
	}

	// a count above the cap leaves the cap alone
	srv2, hits2 := pageServer(t, 9, `"page_count":50,`)
	src2 := mustSource(t, `{"name":"t",%s,"url":%q,
		"pagination":{"max_pages":2,"pages_path":"page_count"}}`,
		jsonExtract, srv2.URL+"/?p={page}")
	if _, errs := run(t, src2); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := hits2.Load(); got != 2 {
		t.Fatalf("pages fetched = %d, want 2", got)
	}
}

func TestPaginationStopsOnEmptyPage(t *testing.T) {
	// the empty page is fetched, contributes nothing and ends the walk
	srv, hits := pageServer(t, 2, "")
	src := mustSource(t, `{"name":"t",%s,"url":%q,"pagination":{"max_pages":10}}`,
		jsonExtract, srv.URL+"/?p={page}")

	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("pages fetched = %d, want 3", got)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %v, want 2", ips(recs))
	}
}

func TestPaginationOffsetStartAndStep(t *testing.T) {
	// proxydb's shape: offset paginated from 0 in steps of 30
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off := r.URL.Query().Get("o")
		seen = append(seen, off)
		if len(seen) > 3 {
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		fmt.Fprintf(w, `{"items":[{"ip":"1.2.3.%s","port":8080}]}`, off)
	}))
	defer srv.Close()

	src := mustSource(t, `{"name":"t",%s,"url":%q,
		"pagination":{"start":0,"step":30,"max_pages":10}}`,
		jsonExtract, srv.URL+"/?o={offset}")
	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got, want := strings.Join(seen, ","), "0,30,60,90"; got != want {
		t.Fatalf("offsets = %s, want %s", got, want)
	}
	if got := strings.Join(ips(recs), " "); got != "1.2.3.0:8080 1.2.3.30:8080 1.2.3.60:8080" {
		t.Fatalf("records = %s", got)
	}
}

func TestBudgetExceeded(t *testing.T) {
	// an exhausted budget is caught before the request, so the message names the
	// page that was never fetched
	srv, hits := pageServer(t, 9, "")
	src := mustSource(t, `{"name":"slow",%s,"url":%q,"budget_s":0,
		"pagination":{"max_pages":3}}`, jsonExtract, srv.URL+"/?p={page}")

	recs, errs := run(t, src)
	if len(recs) != 0 || hits.Load() != 0 {
		t.Fatalf("fetched %d records over %d requests, want none", len(recs), hits.Load())
	}
	if len(errs) != 1 || errs[0] != "slow: budget exceeded at page 1" {
		t.Fatalf("errors = %v", errs)
	}
}

func TestFallbackURL(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "1.2.3.7:1080\n")
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", 404)
	}))
	defer bad.Close()

	const text = `"extract":{"regex":"(?P<ip>[\\d.]+):(?P<port>\\d+)"}`

	// a successful fallback is silent
	src := mustSource(t, `{"name":"t",%s,"url":%q,"fallback_url":%q}`,
		text, bad.URL, good.URL)
	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := ips(recs); len(got) != 1 || got[0] != "1.2.3.7:1080" {
		t.Fatalf("records = %v", got)
	}

	// when both fail only the last candidate is reported
	src = mustSource(t, `{"name":"t",%s,"url":%q,"fallback_url":%q}`, text, bad.URL, bad.URL)
	recs, errs = run(t, src)
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "t: fetch failed: 404 Client Error") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestURLsPaginateOnlyTemplatedEntries(t *testing.T) {
	// myproxy's shape: one templated list url alongside fixed ones. the fixed
	// entries must be fetched exactly once each even though pagination is on.
	reqs := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs <- r.URL.String()
		if strings.HasPrefix(r.URL.Path, "/list") {
			p := r.URL.Query().Get("p")
			if p == "3" {
				fmt.Fprint(w, "")
				return
			}
			fmt.Fprintf(w, "1.2.3.%s:80\n", p)
			return
		}
		fmt.Fprint(w, "5.6.7.1:81\n")
	}))
	defer srv.Close()

	src := mustSource(t, `{"name":"t","extract":{"regex":"(?P<ip>[\\d.]+):(?P<port>\\d+)"},
		"urls":[{"url":%q},{"url":%q,"set":{"protocol":"socks5"}}],
		"pagination":{"max_pages":9}}`,
		srv.URL+"/list?p={page}", srv.URL+"/fixed")

	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	close(reqs)
	var got []string
	for u := range reqs {
		got = append(got, u)
	}
	want := "/list?p=1,/list?p=2,/list?p=3,/fixed"
	if strings.Join(got, ",") != want {
		t.Fatalf("requests = %v, want %s", got, want)
	}
	if got := strings.Join(ips(recs), " "); got != "1.2.3.1:80 1.2.3.2:80 5.6.7.1:81" {
		t.Fatalf("records = %s", got)
	}
	// the fixed entry's set{} still applies
	if p := recs[2].Protocols; len(p) != 1 || p[0] != "socks5" {
		t.Fatalf("protocols = %v, want [socks5]", p)
	}
}

func TestPrefetchHeaderAndAsURL(t *testing.T) {
	// floppydata's shape (a regex into a header) followed by proxybros' (a
	// json_path used as the next url).
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "var cfg = {\n  key: 'Bearer  abc123 '\n}")
	})
	var gotAuth string
	mux.HandleFunc("/discover", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"data":{"href":"/final.txt"}}`)
	})
	mux.HandleFunc("/final.txt", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth carried through", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "1.2.3.9:3128\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := mustSource(t, `{"name":"t","extract":{"regex":"(?P<ip>[\\d.]+):(?P<port>\\d+)"},
		"url":"http://unused.invalid/",
		"prefetch":[
		  {"url":%q,"regex":"key: '([^']+)'","header":"Authorization"},
		  {"url":%q,"json_path":"data.href","as_url":true,"base":%q}
		]}`, srv.URL+"/token", srv.URL+"/discover", srv.URL)

	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// str().strip() runs on the captured group, so the inner double space stays
	if gotAuth != "Bearer  abc123" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if got := ips(recs); len(got) != 1 || got[0] != "1.2.3.9:3128" {
		t.Fatalf("records = %v", got)
	}
}

func TestPrefetchFailureAbortsSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "nothing to match here")
	}))
	defer srv.Close()

	src := mustSource(t, `{"name":"t","url":"http://unused.invalid/",
		"prefetch":[{"url":%q,"regex":"token=(\\w+)","header":"X-Tok"}]}`, srv.URL)
	recs, errs := run(t, src)
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	want := "t: prefetch failed: prefetch step failed for " + srv.URL
	if len(errs) != 1 || errs[0] != want {
		t.Fatalf("errors = %v, want [%s]", errs, want)
	}
}

// TestPrefetchHeaderOnlyKeepsSourceURL covers the branch where the last step is
// not as_url: the steps existed to collect headers and src.url is still the
// thing to fetch.
func TestPrefetchHeaderOnlyKeepsSourceURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/tok", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"t":"s3cret"}`)
	})
	var gotTok string
	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		gotTok = r.Header.Get("X-Tok")
		fmt.Fprint(w, "1.2.3.5:8000\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := mustSource(t, `{"name":"t","extract":{"regex":"(?P<ip>[\\d.]+):(?P<port>\\d+)"},
		"url":%q,"prefetch":[{"url":%q,"json_path":"t","header":"X-Tok"}]}`,
		srv.URL+"/list", srv.URL+"/tok")
	recs, errs := run(t, src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if gotTok != "s3cret" {
		t.Fatalf("X-Tok = %q", gotTok)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %v", ips(recs))
	}
}

func TestFlowDispatch(t *testing.T) {
	src := mustSource(t, `{"name":"t","flow":"demo","budget_s":42}`)
	var gotDeadline bool
	f := &Fetcher{Flows: map[string]FlowFunc{
		"demo": func(_ context.Context, fl *Flow) ([]*extract.Record, []string) {
			gotDeadline = !fl.Deadline.IsZero()
			fl.State.Request("http://flow/")
			return []*extract.Record{{IP: "1.2.3.1", Port: 1}}, nil
		},
	}}
	st := &State{}
	recs, errs := f.Source(context.Background(), src, st)
	if len(errs) != 0 || len(recs) != 1 {
		t.Fatalf("recs=%v errs=%v", ips(recs), errs)
	}
	if !gotDeadline {
		t.Fatal("flow got no deadline")
	}
	if snap := st.Snapshot(); snap.Requests != 1 || snap.URL != "http://flow/" {
		t.Fatalf("state = %+v", snap)
	}

	// an unregistered flow is an error, not a panic
	_, errs = (&Fetcher{}).Source(context.Background(), src, nil)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "t: flow 'demo' failed:") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestParseFailureFormat(t *testing.T) {
	// a text source with no regex: python raises KeyError('regex'), which the
	// readme shows as "<name>: parse failed: 'regex'"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "1.2.3.4:80\n")
	}))
	defer srv.Close()

	src := mustSource(t, `{"name":"t","url":%q}`, srv.URL)
	_, errs := run(t, src)
	if len(errs) != 1 || errs[0] != "t: parse failed: 'regex'" {
		t.Fatalf("errors = %v", errs)
	}

	src = mustSource(t, `{"name":"t","url":%q,"pagination":{"max_pages":2}}`, srv.URL)
	_, errs = run(t, src)
	if len(errs) != 1 || errs[0] != "t page=1: parse failed: 'regex'" {
		t.Fatalf("errors = %v", errs)
	}
}

func TestSubstPage(t *testing.T) {
	body := map[string]any{
		"page":  "{page}",
		"off":   "{offset}",
		"other": "page={page}",
		"keep":  "x",
	}
	got, ok := substPage(body, 7).(map[string]any)
	if !ok {
		t.Fatal("not a map")
	}
	if got["page"] != 7 || got["off"] != 7 {
		t.Fatalf("placeholders not substituted: %#v", got)
	}
	// the match is exact, so an embedded token is left as-is
	if got["other"] != "page={page}" || got["keep"] != "x" {
		t.Fatalf("non-placeholders changed: %#v", got)
	}
	// the original is untouched, so a requeued source sees the template again
	if body["page"] != "{page}" {
		t.Fatal("input mutated")
	}
	if substPage("raw", 1) != "raw" || substPage(nil, 1) != nil {
		t.Fatal("non-dict body should pass through")
	}
}

func TestPyInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
		ok   bool
	}{
		{json.Number("42"), 42, true},
		{json.Number("42.9"), 42, true},   // int() truncates
		{json.Number("-42.9"), -42, true}, // toward zero, not down
		{json.Number("1e3"), 1000, true},
		{json.Number("oops"), 0, false},
		{float64(7.9), 7, true},
		{"123", 123, true},
		{" 123 ", 123, true}, // int() tolerates surrounding space
		{"12.5", 0, false},   // int("12.5") raises
		{"", 0, false},
		{true, 1, true},
		{nil, 0, false},
		{[]any{1}, 0, false},
	}
	for _, c := range cases {
		got, ok := pyInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("pyInt(%#v) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestStateSnapshot(t *testing.T) {
	var st *State
	if snap := st.Snapshot(); snap.Requests != 0 || snap.Elapsed != 0 {
		t.Fatalf("nil state = %+v", snap)
	}

	st = &State{}
	// before Start, elapsed is zero rather than time since the epoch
	if snap := st.Snapshot(); snap.Elapsed != 0 {
		t.Fatalf("elapsed before Start = %v", snap.Elapsed)
	}
	st.Start()
	st.Request("http://a/")
	st.Request("http://b/")
	st.setPage(3)
	snap := st.Snapshot()
	if snap.Requests != 2 || snap.URL != "http://b/" || snap.Page != 3 {
		t.Fatalf("state = %+v", snap)
	}
	if snap.Elapsed <= 0 {
		t.Fatalf("elapsed = %v, want > 0", snap.Elapsed)
	}
	// a requeued source starts over
	st.Start()
	if snap := st.Snapshot(); snap.Requests != 0 || snap.Page != 0 || snap.URL != "" {
		t.Fatalf("after restart = %+v", snap)
	}
}
