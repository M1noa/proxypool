package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/fetch"
)

// flowFor builds the argument fetch.Source hands a flow. budget is how much of
// the source budget is left; a negative value is an exhausted one.
func flowFor(name string, budget time.Duration) *fetch.Flow {
	var src config.Source
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"name":%q,"flow":%q}`, name, name)), &src); err != nil {
		panic(err)
	}
	st := &fetch.State{}
	st.Start()
	return &fetch.Flow{Src: &src, Deadline: time.Now().Add(budget), State: st}
}

// recorder collects the paths a flow requests, in order.
type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, s)
}

func (r *recorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.seen, "\n")
}

func ips(recs []*extract.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = fmt.Sprintf("%s:%d", r.IP, r.Port)
	}
	return out
}

func TestClean(t *testing.T) {
	cases := [][2]string{
		{"<a href='x'>1.2.3.4</a>", "1.2.3.4"},
		{"  8080\n\t", "8080"},
		{"elite\n  proxy", "elite proxy"},
		// entities are never decoded, so this is text and not a space
		{"&nbsp;", "&nbsp;"},
		// a real nbsp is whitespace to python's \s and has to be to RE2's too
		{"a 　b", "a b"},
		{"<td><span>US</span> </td>", "US"},
		{"", ""},
	}
	for _, c := range cases {
		if got := clean(c[0]); got != c[1] {
			t.Errorf("clean(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestCells(t *testing.T) {
	html := `<table>
	<tr class="x"><td>a</td><td colspan=2>b</td></tr>
	<tr><th>head</th></tr>
	<tr><td><b>c</b></td></tr>
	</table>`
	got := cells(html)
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	if strings.Join(got[0], ",") != "a,b" {
		t.Errorf("row 0 = %v", got[0])
	}
	// a header row has no <td> at all, which is what the length guards drop
	if len(got[1]) != 0 {
		t.Errorf("row 1 = %v, want empty", got[1])
	}
	if strings.Join(got[2], ",") != "<b>c</b>" {
		t.Errorf("row 2 = %v", got[2])
	}
}

func TestSixsixdailiMatrix(t *testing.T) {
	var rec recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rec.add(q.Get("protocol") + "/" + q.Get("anonymity"))
		// the second row has no port, so the f-string renders "None" and the
		// record is dropped on normalization
		fmt.Fprintf(w, `{"data":[{"ip":"1.2.3.%d","port":8080},{"ip":"5.6.7.1"}]}`,
			len(rec.seen))
	}))
	defer srv.Close()
	dailiAPI = srv.URL + "//"

	recs, errs := sixsixdaili(context.Background(), flowFor("sixsixdaili", time.Minute))
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// 4 protocols x 3 anonymity values, in declaration order. the url carries
	// the anonymity encoded, so the server sees it decoded.
	want := strings.Join([]string{
		"HTTP/高匿", "HTTP/普匿", "HTTP/透明",
		"HTTPS/高匿", "HTTPS/普匿", "HTTPS/透明",
		"Socks4/高匿", "Socks4/普匿", "Socks4/透明",
		"Socks5/高匿", "Socks5/普匿", "Socks5/透明",
	}, "\n")
	if got := rec.joined(); got != want {
		t.Fatalf("requests =\n%s\nwant\n%s", got, want)
	}
	if len(recs) != 12 {
		t.Fatalf("records = %v, want 12", ips(recs))
	}
	first, last := recs[0], recs[11]
	if first.Country != "CN" || first.Anonymity != "elite" ||
		strings.Join(first.Protocols, ",") != "http" {
		t.Errorf("first = %+v", first)
	}
	if last.Anonymity != "transparent" || strings.Join(last.Protocols, ",") != "socks5" {
		t.Errorf("last = %+v", last)
	}
}

func TestSixsixdailiBudgetExceeded(t *testing.T) {
	var rec recorder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()
	dailiAPI = srv.URL + "//"

	recs, errs := sixsixdaili(context.Background(), flowFor("sixsixdaili", -time.Second))
	// the budget is checked before the first combo and the flow returns rather
	// than continuing, so there is exactly one error and no request
	if len(errs) != 1 || errs[0] != "sixsixdaili: budget exceeded" {
		t.Fatalf("errors = %v", errs)
	}
	if len(recs) != 0 || rec.joined() != "" {
		t.Fatalf("recs=%v requests=%q", ips(recs), rec.joined())
	}
}

func TestSixsixdailiPerComboErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("protocol") == "HTTP" {
			http.Error(w, "nope", 404)
			return
		}
		fmt.Fprint(w, `[1,2,3]`)
	}))
	defer srv.Close()
	dailiAPI = srv.URL + "//"

	recs, errs := sixsixdaili(context.Background(), flowFor("sixsixdaili", time.Minute))
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	// every combo fails and each is labelled with its own pair
	if len(errs) != 12 {
		t.Fatalf("errors = %v, want 12", errs)
	}
	if !strings.HasPrefix(errs[0], "sixsixdaili HTTP/elite: 404 Client Error") {
		t.Errorf("errs[0] = %q", errs[0])
	}
	// a fetch that works but answers with a list, not an object
	if errs[3] != "sixsixdaili HTTPS/elite: response is not an object" {
		t.Errorf("errs[3] = %q", errs[3])
	}
}

// TestSixsixdailiTolerantData covers the shapes python swallows without an
// error: no data key, a null one, and one that is not a list.
func TestSixsixdailiTolerantData(t *testing.T) {
	bodies := []string{`{}`, `{"data":null}`, `{"data":{"a":1}}`, `{"data":["x",{"ip":""}]}`}
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, bodies[n%len(bodies)])
		n++
	}))
	defer srv.Close()
	dailiAPI = srv.URL + "//"

	recs, errs := sixsixdaili(context.Background(), flowFor("sixsixdaili", time.Minute))
	if len(errs) != 0 || len(recs) != 0 {
		t.Fatalf("recs=%v errs=%v", ips(recs), errs)
	}
}

const proxyhubRow = `<tr><td>flag</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`

func TestProxyhubDiscoversLinks(t *testing.T) {
	var rec recorder
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		// gb twice to prove the dedup, and us in both spellings to prove the
		// dedup is on the whole href and keeps them apart
		fmt.Fprint(w, `<a href="/en/us-free-proxy-list">us</a>
			<a href="/en/gb-free-proxy-list.html">gb</a>
			<a href="/en/gb-free-proxy-list.html">gb again</a>
			<a href="/en/us-free-proxy-list.htm">us htm</a>
			<a href="/en/xxx-free-proxy-list">too long, ignored</a>`)
	})
	page := func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		fmt.Fprintf(w, "<table>"+
			proxyhubRow+ // good
			proxyhubRow+ // bad ip
			proxyhubRow+ // empty port
			"<tr><td>a</td><td>b</td><td>c</td></tr>"+ // too few cells
			"</table>",
			"1.2.3.4", "8080", "HTTP", "Elite",
			"not-an-ip", "8080", "HTTP", "Elite",
			"5.6.7.1", " ", "HTTP", "Elite")
	}
	for _, p := range []string{"/en/us-free-proxy-list", "/en/gb-free-proxy-list.html",
		"/en/us-free-proxy-list.htm"} {
		mux.HandleFunc(p, page)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	proxyhubHome = srv.URL + "/"

	recs, errs := proxyhub(context.Background(), flowFor("proxyhub", time.Minute))
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// sorted, so the order is reproducible where python's set is not
	want := "/\n/en/gb-free-proxy-list.html\n/en/us-free-proxy-list\n/en/us-free-proxy-list.htm"
	if got := rec.joined(); got != want {
		t.Fatalf("requests =\n%s\nwant\n%s", got, want)
	}
	if got := strings.Join(ips(recs), " "); got != "1.2.3.4:8080 1.2.3.4:8080 1.2.3.4:8080" {
		t.Fatalf("records = %s", got)
	}
	if recs[0].Country != "GB" || recs[1].Country != "US" || recs[2].Country != "US" {
		t.Fatalf("countries = %s %s %s", recs[0].Country, recs[1].Country, recs[2].Country)
	}
	if recs[0].Anonymity != "elite" || strings.Join(recs[0].Protocols, ",") != "http" {
		t.Fatalf("first = %+v", recs[0])
	}
}

func TestProxyhubHomeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", 404)
	}))
	defer srv.Close()
	proxyhubHome = srv.URL + "/"

	recs, errs := proxyhub(context.Background(), flowFor("proxyhub", time.Minute))
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "proxyhub: 404 Client Error") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestProxyhubBudgetStopsBetweenLinks(t *testing.T) {
	var rec recorder
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		// the home page alone spends the budget. it still completes: a deadline
		// bounds the retry loop, not a request already in flight, and the 1s
		// timeout floor leaves room for this.
		time.Sleep(250 * time.Millisecond)
		fmt.Fprint(w, `<a href="/en/us-free-proxy-list">us</a>`)
	})
	mux.HandleFunc("/en/us-free-proxy-list", func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path)
		fmt.Fprint(w, "<table></table>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	proxyhubHome = srv.URL + "/"

	recs, errs := proxyhub(context.Background(), flowFor("proxyhub", 50*time.Millisecond))
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	if len(errs) != 1 || errs[0] != "proxyhub: budget exceeded" {
		t.Fatalf("errors = %v", errs)
	}
	if got := rec.joined(); got != "/" {
		t.Fatalf("requests = %q, want only the home page", got)
	}
}

func TestProxynovaEvalsIPCell(t *testing.T) {
	const row = `<tr><td>%s</td><td>%s</td><td>c</td><td>d</td><td>e</td><td>f</td><td>%s</td></tr>`
	write := func(js string) string {
		return `<abbr><script>document.write(` + js + `)</script></abbr>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<table>"+
			// the real shape: a concatenation split to defeat scrapers
			fmt.Sprintf(row, write(`'1.2.'+'3.4'`), "8080", "Elite proxy")+
			// port is not numeric
			fmt.Sprintf(row, write(`'1.2.'+'3.5'`), "80x80", "Elite")+
			// no document.write at all
			fmt.Sprintf(row, `<span>1.2.3.6</span>`, "8080", "Elite")+
			// the expression throws
			fmt.Sprintf(row, write(`nope.nope`), "8080", "Elite")+
			// the expression evaluates but is not an ip
			fmt.Sprintf(row, write(`'hello'`), "8080", "Elite")+
			// too few cells
			`<tr><td>`+write(`'1.2.'+'3.7'`)+`</td><td>8080</td></tr>`+
			"</table>")
	}))
	defer srv.Close()
	proxynovaHome = srv.URL + "/"

	recs, errs := proxynova(context.Background(), flowFor("proxynova", time.Minute))
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := strings.Join(ips(recs), " "); got != "1.2.3.4:8080" {
		t.Fatalf("records = %s", got)
	}
	if recs[0].Anonymity != "elite" || strings.Join(recs[0].Protocols, ",") != "http" {
		t.Fatalf("record = %+v", recs[0])
	}
}

func TestProxynovaFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", 404)
	}))
	defer srv.Close()
	proxynovaHome = srv.URL + "/"

	recs, errs := proxynova(context.Background(), flowFor("proxynova", time.Minute))
	if len(recs) != 0 {
		t.Fatalf("records = %v", ips(recs))
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "proxynova: 404 Client Error") {
		t.Fatalf("errors = %v", errs)
	}
}

// TestEvalIPInterrupt proves a runaway expression in the page cannot wedge a
// pool worker for the rest of the run.
func TestEvalIPInterrupt(t *testing.T) {
	vm := goja.New()
	start := time.Now()
	if _, err := evalIP(vm, "while(true){}"); err == nil {
		t.Fatal("expected an interrupt error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("interrupt took %s", d)
	}
	// the runtime is reusable afterwards, which is what lets one vm serve the
	// whole table
	if ip, err := evalIP(vm, `'1.2.'+'3.4'`); err != nil || ip != "1.2.3.4" {
		t.Fatalf("ip=%q err=%v", ip, err)
	}
}

func TestRunnerTimeoutShrinksWithBudget(t *testing.T) {
	fl := flowFor("t", time.Hour)
	r := newRunner(fl)
	defer r.c.CloseIdle()
	// well inside the budget, the flow default caps it
	if got := min(r.max, max(time.Second, r.remain())); got != flowTimeout {
		t.Fatalf("timeout = %s, want %s", got, flowTimeout)
	}
	// nearly out of budget, the remainder caps it, with a one second floor
	r.deadline = time.Now().Add(-time.Minute)
	if got := min(r.max, max(time.Second, r.remain())); got != time.Second {
		t.Fatalf("timeout = %s, want 1s", got)
	}
	// no deadline at all is python's 60s fallback, still capped by the default
	r.deadline = time.Time{}
	if got := r.remain(); got != 60*time.Second {
		t.Fatalf("remain = %s, want 60s", got)
	}
}

func TestTableCoversEveryConfiguredFlow(t *testing.T) {
	srcs, err := config.Load("../../sources.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for i := range srcs {
		if srcs[i].Flow == "" {
			continue
		}
		n++
		if Table[srcs[i].Flow] == nil {
			t.Errorf("source %q names unregistered flow %q", srcs[i].Name, srcs[i].Flow)
		}
	}
	if n != 3 {
		t.Errorf("flow sources = %d, want 3", n)
	}
}
