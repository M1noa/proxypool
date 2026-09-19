// package httpx is the source-fetching http layer: a per-source client with
// the header stack lib/util.make_session builds, and the retry loop from
// lib/util.request.
package httpx

import (
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/M1noa/proxypool/internal/config"
)

// Egress deals out proxy urls for retries. implemented by internal/egress;
// an interface here so httpx does not import it.
type Egress interface {
	Next() *url.URL
}

// BrowserUA is the ua antibot sources get instead of proxypool/1.0.
const BrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// BrowserHeaders mirrors lib/util.browser_headers.
func BrowserHeaders() map[string]string {
	return map[string]string{
		"user-agent": BrowserUA,
		"accept": "text/html,application/xhtml+xml,application/xml;q=0.9," +
			"image/avif,image/webp,*/*;q=0.8",
		"accept-language":           "en-US,en;q=0.9",
		"cache-control":             "no-cache",
		"pragma":                    "no-cache",
		"upgrade-insecure-requests": "1",
	}
}

// Client is one source's http session: a connection pool plus the header set
// every request starts from.
type Client struct {
	hc      *http.Client
	headers http.Header
	timeout time.Duration

	// Egress, when non-nil, carries retry attempts (2+) through rotating
	// pool proxies. attempt 1 always goes direct.
	Egress Egress

	mu      sync.Mutex
	proxied map[string]*http.Client
}

// New builds the client for a source. header precedence matches
// lib/util.make_session: proxypool/1.0, then the browser set when antibot is
// on, then the source's own headers last.
//
// requests applies its `timeout` per socket operation, not to the whole
// request, so a slow but steady 500k-line download completes. a single
// context deadline here would kill it. connect and time-to-first-byte are
// capped instead, and the body read is bounded by the source budget.
func New(src *config.Source, timeout time.Duration) *Client {
	h := http.Header{}
	// requests' own session default. go sets Accept-Encoding itself and
	// decompresses transparently, which is what requests does too.
	h.Set("Accept", "*/*")
	h.Set("User-Agent", "proxypool/1.0")
	if src != nil && src.Antibot {
		for k, v := range BrowserHeaders() {
			h.Set(k, v)
		}
	}
	if src != nil {
		for k, v := range src.Headers {
			h.Set(k, v)
		}
	}
	// requests.Session keeps a cookie jar, and proxybros only serves its export
	// to the session that ran the prefetch POST. without a jar that request
	// comes back 200 with an empty body.
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &Client{
		hc:      &http.Client{Transport: newTransport(timeout, nil), Jar: jar},
		headers: h,
		timeout: timeout,
	}
}

// newTransport builds the session transport. proxy nil means direct (plus
// environment); otherwise every request on it goes through that proxy.
func newTransport(timeout time.Duration, proxy *url.URL) *http.Transport {
	var pf func(*http.Request) (*url.URL, error)
	if proxy == nil {
		pf = http.ProxyFromEnvironment
	} else {
		pf = http.ProxyURL(proxy)
	}
	return &http.Transport{
		Proxy:                 pf,
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		// no ForceAttemptHTTP2: a custom DialContext already suppresses the
		// h2 upgrade, and urllib3 only ever speaks http/1.1. letting go
		// negotiate h2 would change what some sources return.
	}
}

// forProxy returns a client sharing this session's jar that sends everything
// through u. one cached client per proxy url: connections cannot be shared
// across proxies anyway, and the pool is small, so this stays bounded.
func (c *Client) forProxy(u *url.URL) *http.Client {
	key := u.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	if hc, ok := c.proxied[key]; ok {
		return hc
	}
	hc := &http.Client{Transport: newTransport(c.timeout, u), Jar: c.hc.Jar}
	if c.proxied == nil {
		c.proxied = map[string]*http.Client{}
	}
	c.proxied[key] = hc
	return hc
}

// CloseIdle releases the client's pooled connections.
func (c *Client) CloseIdle() {
	if tr, ok := c.hc.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// decodeBody reproduces requests' Response.text.
//
// the rules matter for parity, not pedantry: a chinese source served as
// text/html with no charset is decoded latin-1 by requests, so its 高匿
// anonymity markers arrive mojibaked and normalize_anon misses them. decoding
// utf-8 here would produce *better* output than the python and break the
// diff.
func decodeBody(contentType string, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	media, charset := parseContentType(contentType)
	switch {
	case charset != "":
		// unknown label: requests falls back to utf-8 with replacement
		if enc, err := htmlindex.Get(charset); err == nil && enc != nil {
			if s, err := enc.NewDecoder().Bytes(body); err == nil {
				return sanitize(string(s))
			}
		}
	case strings.Contains(media, "text"):
		return latin1(body)
	}
	// application/json, and the no-content-type case where requests runs
	// charset detection. every source in that bucket serves utf-8.
	return sanitize(string(body))
}

// parseContentType splits a content-type header into a lowercased media type
// and its charset param, matching requests' _parse_content_type_header.
func parseContentType(ct string) (media, charset string) {
	parts := strings.Split(ct, ";")
	media = strings.ToLower(strings.TrimSpace(parts[0]))
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "charset") {
			continue
		}
		charset = strings.Trim(strings.TrimSpace(v), `'"`)
	}
	return media, charset
}

// latin1 decodes iso-8859-1, where every byte is the code point of the same
// value.
func latin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// sanitize replaces invalid utf-8, standing in for python's errors="replace".
// python emits one U+FFFD per maximal invalid subpart and this emits one per
// run, so a long invalid stretch differs in replacement count.
func sanitize(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}
