// package fetch drives the source layer: one source's entries and pages
// (source.go), and the two worker pools that run all of them (pool.go).
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M1noa/proxypool/internal/config"
	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/httpx"
)

// DefaultMaxPages is fetch_source's max_pages_default: the cap for a paginated
// source that names no max_pages of its own.
const DefaultMaxPages = 20

// State is the watchdog's window into a running fetch. python shares a plain
// dict across threads and leans on the gil to keep it coherent; here the
// reader is a separate goroutine, so the fields are behind a mutex.
type State struct {
	mu       sync.Mutex
	start    time.Time
	requests int
	page     int
	url      string
}

// Snap is a consistent read of a State.
type Snap struct {
	Elapsed  time.Duration
	Requests int
	// Page is 0 both before the first page completes and for the two
	// offset-paginated sources that start at 0. python cannot tell those apart
	// either — it renders `page or '-'`.
	Page int
	URL  string
}

// Start resets the state as a worker picks the job up, so elapsed time
// excludes however long the source sat in the queue.
func (s *State) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start, s.requests, s.page, s.url = time.Now(), 0, 0, ""
}

func (s *State) Snapshot() Snap {
	if s == nil {
		return Snap{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var elapsed time.Duration
	if !s.start.IsZero() {
		elapsed = time.Since(s.start)
	}
	return Snap{Elapsed: elapsed, Requests: s.requests, Page: s.page, URL: s.url}
}

// Request records an outgoing url. exported because a flow does its own
// fetching and still has to feed the watchdog.
func (s *State) Request(url string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.url = url
}

func (s *State) setPage(p int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page = p
}

// FlowFunc is a hand-written source: one that config cannot express, so it
// fetches and normalizes on its own.
type FlowFunc func(context.Context, *Flow) ([]*extract.Record, []string)

// Flow replaces the `dict(src, _deadline=…, _state=…)` copy python hands a
// flow.
type Flow struct {
	Src      *config.Source
	Deadline time.Time
	State    *State
}

// Fetcher holds what fetch_source takes as arguments.
type Fetcher struct {
	// Timeout overrides the per-request timeout. zero uses the source's own,
	// which defaults to 12s; the pipeline always leaves this zero.
	Timeout time.Duration
	// MaxPages is DefaultMaxPages when zero.
	MaxPages int
	// Flows dispatches the hand-written sources. injected rather than imported
	// so internal/flows can depend on this package's State.
	Flows map[string]FlowFunc
}

func (f *Fetcher) maxPages() int {
	if f.MaxPages > 0 {
		return f.MaxPages
	}
	return DefaultMaxPages
}

// Source fetches every entry and page of one source.
//
// failures come back as strings rather than errors because they are collected
// across 150 sources and rendered into the readme verbatim. the formats are
// load-bearing twice over: the readme's error column shows them, and the pool
// decides whether to requeue a source by looking for "429" in them.
func (f *Fetcher) Source(ctx context.Context, src *config.Source, st *State) ([]*extract.Record, []string) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = time.Duration(src.TimeoutS()) * time.Second
	}
	deadline := time.Now().Add(time.Duration(src.Budget()) * time.Second)

	if src.Flow != "" {
		fn := f.Flows[src.Flow]
		if fn == nil {
			// python's FLOWS[…] raises a KeyError inside the same try, so this
			// arrives in the readme as the flow name echoed back at itself
			return nil, []string{fmt.Sprintf("%s: flow '%s' failed: not registered",
				src.Name, src.Flow)}
		}
		return fn(ctx, &Flow{Src: src, Deadline: deadline, State: st})
	}

	client := httpx.New(src, timeout)
	defer client.CloseIdle()

	// multi-entry sources are [{url, set{field: const}}, …]. copied because
	// prefetch rewrites the first url and a requeued source has to see the
	// template again.
	entries := src.URLs
	if len(entries) == 0 {
		entries = []config.URLEntry{{URL: src.URL}}
	}
	entries = append([]config.URLEntry(nil), entries...)

	var extraHeaders map[string]string
	if len(src.Prefetch) > 0 {
		url, h, err := f.prefetch(ctx, src, client, deadline, st)
		if err != nil {
			return nil, []string{fmt.Sprintf("%s: prefetch failed: %v", src.Name, err)}
		}
		entries[0].URL, extraHeaders = url, h
	}

	fetchOnce := func(url string, body any) (string, error) {
		st.Request(url)
		return client.Do(ctx, httpx.Req{
			URL: url, Method: src.HTTPMethod(), Body: body,
			BodyType: src.BodyType, Headers: extraHeaders, Deadline: deadline,
		})
	}

	var recs []*extract.Record
	var errs []string
	pag := src.Pagination

	for i := range entries {
		tmpl := entries[i].URL
		defaults := entries[i].Set
		// a single-url source paginates whenever pagination is configured; a
		// urls[] entry only when its url is actually templated. that is what
		// lets myproxy walk its numbered list pages while fetching its five
		// fixed ones once each.
		paged := pag != nil
		if len(src.URLs) > 0 {
			paged = pag != nil && (strings.Contains(tmpl, "{page}") ||
				strings.Contains(tmpl, "{offset}"))
		}

		if !paged {
			urls := []string{tmpl}
			if i == 0 && src.FallbackURL != "" {
				urls = append(urls, src.FallbackURL)
			}
			var content string
			var ok bool
			for j, url := range urls {
				c, err := fetchOnce(url, src.Body)
				if err == nil {
					content, ok = c, true
					break
				}
				// only the last candidate's failure is reported, so a source
				// that falls back successfully looks clean
				if j == len(urls)-1 {
					errs = append(errs, fmt.Sprintf("%s: fetch failed: %v", src.Name, err))
				}
			}
			if ok {
				// an empty body still parses; ok is not `content != ""`
				batch, err := extract.ParseContent(src, content, defaults)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: parse failed: %v", src.Name, err))
				} else {
					recs = append(recs, batch...)
				}
			}
			continue
		}

		page := pag.StartAt()
		step := pag.StepBy()
		maxPages := pag.MaxPagesOr(f.maxPages())
		delay := time.Duration(pag.DelayMS) * time.Millisecond
		limit := pag.PageSize()

		for n := 0; n < maxPages; n++ {
			if time.Now().After(deadline) {
				errs = append(errs, fmt.Sprintf("%s: budget exceeded at page %d",
					src.Name, page))
				break
			}
			num := strconv.Itoa(page)
			url := strings.ReplaceAll(strings.ReplaceAll(tmpl, "{page}", num), "{offset}", num)
			if delay > 0 {
				// deliberately not clamped to the deadline: python sleeps first
				// and only notices the budget on the next iteration
				if err := nap(ctx, delay); err != nil {
					break
				}
			}
			content, err := fetchOnce(url, substPage(src.Body, page))
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s page=%d: fetch failed: %v",
					src.Name, page, err))
				break
			}
			// the server's own count replaces the configured cap, but only from
			// the first page: total_path can raise it, pages_path only lowers it
			if n == 0 && pag.TotalPath != "" {
				if v := digJSON(content, pag.TotalPath); extract.Truthy(v) {
					if total, ok := pyInt(v); ok && limit != 0 {
						maxPages = max(1, int(math.Ceil(float64(total)/float64(limit))))
					}
				}
			} else if n == 0 && pag.PagesPath != "" {
				if v := digJSON(content, pag.PagesPath); extract.Truthy(v) {
					if pages, ok := pyInt(v); ok {
						maxPages = min(maxPages, max(1, pages))
					}
				}
			}
			batch, err := extract.ParseContent(src, content, defaults)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s page=%d: parse failed: %v",
					src.Name, page, err))
				break
			}
			if len(batch) == 0 {
				break
			}
			recs = append(recs, batch...)
			st.setPage(page)
			page += step
		}
	}
	return recs, errs
}

// prefetch runs the discovery steps before the real fetch and returns the url
// to fetch plus the headers to carry into it.
//
// a step reads one value out of its response with either json_path or regex,
// then stores it as a header, feeds it to the next step's url, or both.
func (f *Fetcher) prefetch(ctx context.Context, src *config.Source, c *httpx.Client,
	deadline time.Time, st *State) (string, map[string]string, error) {

	headers := map[string]string{}
	nextURL := ""
	for _, step := range src.Prefetch {
		u := step.URL
		if u == "" {
			u = nextURL
		}
		st.Request(u)
		// python omits timeout= here, so a prefetch step always gets request()'s
		// 12s default even when the source overrides it. no source overrides it.
		content, err := c.Do(ctx, httpx.Req{
			URL: u, Method: step.Method, Body: step.Body,
			BodyType: step.BodyType, Headers: headers, Deadline: deadline,
		})
		if err != nil {
			return "", nil, err
		}

		var val any
		switch {
		case step.JSONPath != "":
			doc, err := extract.DecodeJSON(content)
			if err != nil {
				return "", nil, err
			}
			val = extract.Dig(doc, step.JSONPath)
		case step.Regex != "":
			// python passes re.S
			re, err := regexp.Compile(`(?s)` + step.Regex)
			if err != nil {
				return "", nil, err
			}
			// indices rather than strings so a group that did not participate
			// stays nil, the way python's m.group() returns None for one — an
			// empty match is a real "" and has to survive
			if loc := re.FindStringSubmatchIndex(content); loc != nil {
				g := step.GroupNum()
				if g < 0 || 2*g+1 >= len(loc) {
					// python's IndexError, whose message this is verbatim
					return "", nil, errors.New("no such group")
				}
				if loc[2*g] >= 0 {
					val = content[loc[2*g]:loc[2*g+1]]
				}
			}
		}
		if val == nil {
			return "", nil, fmt.Errorf("prefetch step failed for %s", u)
		}

		s := extract.PyStrip(extract.PyStr(val))
		if step.Header != "" {
			headers[step.Header] = s
		}
		if step.AsURL {
			if step.Base != "" && strings.HasPrefix(s, "/") {
				s = step.Base + s
			}
			nextURL = s
		}
	}
	// the discovered url is only used when the last step asked for it;
	// otherwise the steps existed to collect headers.
	if src.Prefetch[len(src.Prefetch)-1].AsURL {
		return nextURL, headers, nil
	}
	return src.URL, headers, nil
}

// substPage rewrites a dict body's page placeholders. the match is exact — a
// value that merely contains the token is left alone — and the replacement is
// the number itself, not its string form.
func substPage(body any, page int) any {
	m, ok := body.(map[string]any)
	if !ok {
		return body
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok && (s == "{page}" || s == "{offset}") {
			out[k] = page
			continue
		}
		out[k] = v
	}
	return out
}

// digJSON decodes a body and walks a dotted path, returning nil on any
// failure. python wraps the equivalent in a bare `except Exception: pass`, so
// a source that answers a page request with html simply keeps its configured
// page cap.
func digJSON(content, path string) any {
	doc, err := extract.DecodeJSON(content)
	if err != nil {
		return nil
	}
	return extract.Dig(doc, path)
}

// pyInt is int(v): a float truncates toward zero, a string has to be a whole
// number or python raises. json.Number holds the literal, so it is read as a
// float first — which is what loads() followed by int() does.
func pyInt(v any) (int, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return int(f), true
	case float64:
		return int(t), true
	case int:
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

// nap sleeps unless the context ends first.
func nap(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
