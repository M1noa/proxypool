// package flows carries the three sources sources.jsonc cannot describe: a
// protocol × anonymity matrix against one api, link discovery followed by
// per-country tables, and a table whose ip cells are javascript.
//
// they scrape with regexes rather than an html parser, exactly as lib/flows.py
// does — swapping in goquery would change which malformed rows survive.
package flows

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/fetch"
	"github.com/M1noa/proxypool/internal/httpx"
)

// Table is wired into fetch.Fetcher.Flows. lib/flows.FLOWS also lists
// "spysone", which no source references — dead, so not ported.
var Table = map[string]fetch.FlowFunc{
	"sixsixdaili": sixsixdaili,
	"proxyhub":    proxyhub,
	"proxynova":   proxynova,
}

// flowTimeout is _get's default. note it is 20s, not request()'s 12s, so a
// flow's requests are more patient than a config-driven source's.
const flowTimeout = 20 * time.Second

// the endpoints are hardcoded in lib/flows too — sources.jsonc carries only a
// `home` for these three, and nothing reads it. vars rather than consts so the
// tests can point them at a local server.
var (
	dailiAPI      = "http://api.66daili.com//"
	proxyhubHome  = "https://proxyhub.me/"
	proxynovaHome = "https://www.proxynova.com/proxy-server-list/"
)

// runner is the `dict(src, _deadline=…, _state=…)` python threads through a
// flow: the budget, the watchdog hook, and one client shared across the
// flow's requests.
type runner struct {
	src      *config.Source
	deadline time.Time
	st       *fetch.State
	c        *httpx.Client
	max      time.Duration

	raws []map[string]any
	errs []string
}

func newRunner(fl *fetch.Flow) *runner {
	lim := flowTimeout
	if fl.Src.Timeout != nil {
		lim = time.Duration(*fl.Src.Timeout) * time.Second
	}
	return &runner{
		src:      fl.Src,
		deadline: fl.Deadline,
		st:       fl.State,
		// the client's socket timeouts are fixed at construction, so they are
		// built at the largest value the per-request clamp can return
		c:   httpx.New(fl.Src, lim),
		max: lim,
	}
}

// remain is _remain: how much of the source budget is left, or 60s when a flow
// is called without one.
func (r *runner) remain() time.Duration {
	if r.deadline.IsZero() {
		return 60 * time.Second
	}
	return max(0, time.Until(r.deadline))
}

// get is _get: report the url to the watchdog, then fetch with a timeout that
// shrinks as the budget drains but never drops below a second.
func (r *runner) get(ctx context.Context, url string) (string, error) {
	r.st.Request(url)
	return r.c.Do(ctx, httpx.Req{
		URL:      url,
		Deadline: r.deadline,
		Timeout:  min(r.max, max(time.Second, r.remain())),
	})
}

// records normalizes what the flow collected. python's _records also returns
// the errors normalization raised, and all three live flows throw that half
// away, so it is not collected here either.
func (r *runner) records() ([]*extract.Record, []string) {
	out := make([]*extract.Record, 0, len(r.raws))
	for _, raw := range r.raws {
		if rec := extract.NormRecord(raw, r.src, r.src.Protocol); rec != nil {
			out = append(out, rec)
		}
	}
	return out, r.errs
}

var (
	reTR  = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	reTD  = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	reTag = regexp.MustCompile(`<[^>]+>`)
	// python's \s on a str pattern is the unicode whitespace property, which
	// covers the separators and the wide spaces an html table can carry. RE2's
	// own \s is only [\t\n\f\r ].
	reWS = regexp.MustCompile(`[\t\n\v\f\r \x1c-\x1f\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]+`)
	// re.match anchors the front; the trailing \n? is python's $, which also
	// matches before a final newline.
	reIPv4 = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3}){3}\n?$`)
	rePort = regexp.MustCompile(`^\d{1,5}\n?$`)
)

// clean strips tags out of a table cell and collapses its whitespace. entities
// are left encoded, so a cell of &nbsp; is the literal text, not a space.
func clean(td string) string {
	return extract.PyStrip(reWS.ReplaceAllString(reTag.ReplaceAllString(td, ""), " "))
}

// cells returns the <td> contents of every <tr> in a document, in order.
func cells(html string) [][]string {
	rows := reTR.FindAllStringSubmatch(html, -1)
	out := make([][]string, 0, len(rows))
	for _, tr := range rows {
		tds := reTD.FindAllStringSubmatch(tr[1], -1)
		row := make([]string, len(tds))
		for i, td := range tds {
			row[i] = td[1]
		}
		out = append(out, row)
	}
	return out
}

// ---- 66daili: cn api, proto × anonymity matrix ----------------------------

// the api takes its anonymity filter as url-encoded chinese. kept encoded
// because that is what goes on the wire; the value beside it is what the
// record carries.
var dailiAnon = []struct{ enc, name string }{
	{"%E9%AB%98%E5%8C%BF", "elite"},
	{"%E6%99%AE%E5%8C%BF", "anonymous"},
	{"%E9%80%8F%E6%98%8E", "transparent"},
}

func sixsixdaili(ctx context.Context, fl *fetch.Flow) ([]*extract.Record, []string) {
	r := newRunner(fl)
	defer r.c.CloseIdle()

	for _, proto := range []string{"HTTP", "HTTPS", "Socks4", "Socks5"} {
		for _, anon := range dailiAnon {
			if r.remain() <= 0 {
				// python returns rather than breaks, so the remaining combos are
				// abandoned and only one budget error is reported
				r.errs = append(r.errs, r.src.Name+": budget exceeded")
				return r.records()
			}
			url := fmt.Sprintf("%s?num=60&anonymity=%s&protocol=%s&format=json&page=1",
				dailiAPI, anon.enc, proto)

			txt, err := r.get(ctx, url)
			if err == nil {
				var doc any
				if doc, err = extract.DecodeJSON(txt); err == nil {
					err = r.dailiRows(doc, proto, anon.name)
				}
			}
			if err != nil {
				// one try wraps the fetch, the decode and the walk in python, so
				// all three arrive under the same label
				r.errs = append(r.errs, fmt.Sprintf("%s %s/%s: %v",
					r.src.Name, proto, anon.name, err))
			}
		}
	}
	return r.records()
}

func (r *runner) dailiRows(doc any, proto, anon string) error {
	obj, ok := doc.(map[string]any)
	if !ok {
		// python calls .get on it and the AttributeError lands in the readme
		return errors.New("response is not an object")
	}
	// a `data` that is absent, null or not a list yields nothing and is not an
	// error: python's `or []` covers the first two and iterating a dict of keys
	// fails the isinstance check on every one.
	rows, _ := obj["data"].([]any)
	for _, row := range rows {
		item, ok := row.(map[string]any)
		if !ok || !extract.Truthy(item["ip"]) {
			continue
		}
		// a missing port renders as "None" and the record is dropped later,
		// which is what the f-string does
		r.raws = append(r.raws, map[string]any{
			"ip":        extract.PyStr(item["ip"]) + ":" + extract.PyStr(item["port"]),
			"protocol":  strings.ToLower(proto),
			"country":   "CN",
			"anonymity": anon,
		})
	}
	return nil
}

// ---- proxyhub: country links -> tables -------------------------------------

var reProxyhubLink = regexp.MustCompile(`href="(/en/([a-z]{2})-free-proxy-list(?:\.html?)?)"`)

func proxyhub(ctx context.Context, fl *fetch.Flow) ([]*extract.Record, []string) {
	r := newRunner(fl)
	defer r.c.CloseIdle()

	home, err := r.get(ctx, proxyhubHome)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("proxyhub: %v", err))
		return r.records()
	}

	// python collects these into a set and iterates it, so its order is
	// arbitrary between runs; sorted here so a run is reproducible. dedup is on
	// the whole href, which keeps /en/us-free-proxy-list and its .html twin as
	// two separate pages, exactly as the set does.
	type link struct{ href, country string }
	seen := map[string]bool{}
	var links []link
	for _, m := range reProxyhubLink.FindAllStringSubmatch(home, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		links = append(links, link{m[1], strings.ToUpper(m[2])})
	}
	slices.SortFunc(links, func(a, b link) int { return strings.Compare(a.href, b.href) })

	for _, l := range links {
		if r.remain() <= 0 {
			r.errs = append(r.errs, "proxyhub: budget exceeded")
			break
		}
		html, err := r.get(ctx, strings.TrimSuffix(proxyhubHome, "/")+l.href)
		if err != nil {
			r.errs = append(r.errs, fmt.Sprintf("proxyhub %s: %v", l.href, err))
			continue
		}
		for _, tds := range cells(html) {
			if len(tds) < 5 {
				continue
			}
			ip, port := clean(tds[1]), clean(tds[2])
			if !reIPv4.MatchString(ip) || port == "" {
				continue
			}
			r.raws = append(r.raws, map[string]any{
				"ip":        ip + ":" + port,
				"protocol":  strings.ToLower(clean(tds[3])),
				"country":   l.country,
				"anonymity": strings.ToLower(clean(tds[4])),
			})
		}
	}
	return r.records()
}

// ---- proxynova: homepage table ---------------------------------------------

var reDocWrite = regexp.MustCompile(`(?s)document\.write\((.+?)\)</script>`)

func proxynova(ctx context.Context, fl *fetch.Flow) ([]*extract.Record, []string) {
	r := newRunner(fl)
	defer r.c.CloseIdle()

	html, err := r.get(ctx, proxynovaHome)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("proxynova: %v", err))
		return r.records()
	}

	// the ip cell is a document.write of a concatenated expression, so reading
	// it means running it
	vm := goja.New()
	for _, tds := range cells(html) {
		if len(tds) < 7 {
			continue
		}
		m := reDocWrite.FindStringSubmatch(tds[0])
		port := clean(tds[1])
		if m == nil || !rePort.MatchString(port) {
			continue
		}
		ip, err := evalIP(vm, extract.PyStrip(m[1]))
		if err != nil || !reIPv4.MatchString(ip) {
			continue
		}
		r.raws = append(r.raws, map[string]any{
			"ip":        ip + ":" + port,
			"protocol":  "http",
			"anonymity": strings.ToLower(clean(tds[6])),
		})
	}
	return r.records()
}

// evalIP runs one cell's expression. the interrupt is insurance python does
// without: a `while(true)` in the page would otherwise hold a pool slot for
// the rest of the run.
func evalIP(vm *goja.Runtime, expr string) (ip string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("eval panicked: %v", p)
		}
	}()
	t := time.AfterFunc(time.Second, func() { vm.Interrupt("timeout") })
	defer func() {
		t.Stop()
		vm.ClearInterrupt()
	}()
	v, err := vm.RunString(expr)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
