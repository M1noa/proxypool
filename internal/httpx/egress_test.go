package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// stubEgress deals one fixed proxy url.
type stubEgress struct{ u *url.URL }

func (s stubEgress) Next() *url.URL { return s.u }

// a forward proxy: whatever absolute url arrives gets fetched and relayed.
func forward(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		out, err := http.NewRequest(r.Method, r.URL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
}

// attempt 1 goes direct; the retry after a 429 goes through egress.
func TestRetryUsesEgress(t *testing.T) {
	var direct, viaProxy atomic.Int32
	target := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if direct.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	})
	proxy := forward(t, &viaProxy)
	pu, _ := url.Parse(proxy.URL)

	c := New(nil, 5*time.Second)
	c.Egress = stubEgress{pu}
	body, err := get(c, target.URL, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if direct.Load() != 2 {
		t.Errorf("target saw %d hits, want 2 (direct + forwarded)", direct.Load())
	}
	if viaProxy.Load() != 1 {
		t.Errorf("proxy saw %d hits, want 1 (the retry)", viaProxy.Load())
	}
}

// without egress every attempt goes direct, as before.
func TestNoEgressStaysDirect(t *testing.T) {
	var direct atomic.Int32
	target := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if direct.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	})
	body, err := get(New(nil, 5*time.Second), target.URL, time.Time{})
	if err != nil || body != "ok" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if direct.Load() != 2 {
		t.Errorf("target saw %d hits, want 2 direct", direct.Load())
	}
}
