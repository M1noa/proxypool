package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/config"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// get is the plain-GET shorthand these tests want; production always goes
// through Do with a fuller Req.
func get(c *Client, url string, deadline time.Time) (string, error) {
	return c.Do(context.Background(), Req{URL: url, Deadline: deadline})
}

func TestHeaderPrecedence(t *testing.T) {
	var got http.Header
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	})
	cases := []struct {
		name string
		src  config.Source
		ua   string
	}{
		{"default", config.Source{}, "proxypool/1.0"},
		{"antibot", config.Source{Antibot: true}, BrowserUA},
		{"override", config.Source{
			Antibot: true,
			Headers: map[string]string{"User-Agent": "custom/9"},
		}, "custom/9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(&tc.src, 5*time.Second)
			if _, err := get(c, s.URL, time.Time{}); err != nil {
				t.Fatal(err)
			}
			if ua := got.Get("User-Agent"); ua != tc.ua {
				t.Errorf("user-agent = %q, want %q", ua, tc.ua)
			}
		})
	}
}

func TestRetriesRetryableStatus(t *testing.T) {
	var n atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// Retry-After: 0 keeps the test instant while exercising the header
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	body, err := get(New(nil, 5*time.Second), s.URL, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if body != "ok" || n.Load() != 3 {
		t.Errorf("body=%q attempts=%d, want \"ok\" after 3", body, n.Load())
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	var n atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := get(New(nil, 5*time.Second), s.URL, time.Time{})
	if int(n.Load()) != MaxAttempts {
		t.Errorf("attempts = %d, want %d", n.Load(), MaxAttempts)
	}
	// fetch_all requeues a source by finding "429" in the message
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("err = %v, want one mentioning 429", err)
	}
}

func TestNonRetryableStatusFailsAtOnce(t *testing.T) {
	var n atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := get(New(nil, 5*time.Second), s.URL, time.Time{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v, want a 404 StatusError", err)
	}
	if n.Load() != 1 {
		t.Errorf("attempts = %d, want 1", n.Load())
	}
}

func TestBudgetStopsBeforeRequest(t *testing.T) {
	var n atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { n.Add(1) })
	_, err := get(New(nil, 5*time.Second), s.URL, time.Now().Add(-time.Second))
	if !errors.Is(err, ErrBudget) {
		t.Errorf("err = %v, want ErrBudget", err)
	}
	if n.Load() != 0 {
		t.Errorf("made %d requests past the deadline, want 0", n.Load())
	}
}

// a non-numeric Retry-After falls back to backoff, and that sleep is clamped
// to the source budget rather than blocking for the full 2s.
func TestBackoffClampedToBudget(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.WriteHeader(http.StatusBadGateway)
	})
	t0 := time.Now()
	_, err := get(New(nil, 5*time.Second), s.URL, time.Now().Add(80*time.Millisecond))
	if !errors.Is(err, ErrBudget) {
		t.Errorf("err = %v, want ErrBudget", err)
	}
	if d := time.Since(t0); d > time.Second {
		t.Errorf("took %v, want the sleep clamped to the budget", d)
	}
}

func TestBackoffWait(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 32}
	for i, w := range want {
		if got := backoffWait(i + 1); got != w*time.Second {
			t.Errorf("backoffWait(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
	if got := backoffWait(20); got != MaxWait {
		t.Errorf("backoffWait(20) = %v, want %v", got, MaxWait)
	}
}

func TestDecodeBody(t *testing.T) {
	utf8CJK := "高匿"
	cases := []struct {
		name, ct, want string
		body           []byte
	}{
		// requests decodes text/* without a charset as latin-1, so utf-8
		// bytes arrive mojibaked. reproduced deliberately: "é" -> "Ã©".
		{"text no charset", "text/html", "Ã©", []byte{0xc3, 0xa9}},
		// 高匿's six utf-8 bytes read as six latin-1 code points, which is
		// why normalize_anon misses the marker on charset-less cn sources.
		{"text no charset cjk", "text/html",
			"\u00e9\u00ab\u0098\u00e5\u008c\u00bf", []byte(utf8CJK)},
		{"text with charset", "text/html; charset=utf-8", utf8CJK, []byte(utf8CJK)},
		{"quoted charset", `text/html; charset="utf-8"`, utf8CJK, []byte(utf8CJK)},
		{"json", "application/json", utf8CJK, []byte(utf8CJK)},
		{"no header", "", utf8CJK, []byte(utf8CJK)},
		{"unknown charset", "text/html; charset=x-nope", utf8CJK, []byte(utf8CJK)},
		{"empty", "text/plain", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeBody(tc.ct, tc.body); got != tc.want {
				t.Errorf("decodeBody(%q) = %q, want %q", tc.ct, got, tc.want)
			}
		})
	}
}

func TestEncodeBody(t *testing.T) {
	buf, ct, err := encodeBody(map[string]any{"action": "x", "n": 2.0}, "form")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/x-www-form-urlencoded" || string(buf) != "action=x&n=2" {
		t.Errorf("form = %q %q", ct, buf)
	}
	buf, ct, err = encodeBody(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" || string(buf) != "{}" {
		t.Errorf("json = %q %q, want an empty object", ct, buf)
	}
}
