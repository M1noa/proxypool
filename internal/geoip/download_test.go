package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gzipBytes is the compressed form of body, for handlers that need to answer
// with a real gzip stream.
func gzipBytes(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func currentAndPrevYM() (cur, prev string) {
	now := time.Now()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	p := firstOfMonth.AddDate(0, 0, -1)
	return fmt.Sprintf("%04d-%02d", now.Year(), now.Month()), fmt.Sprintf("%04d-%02d", p.Year(), p.Month())
}

func TestDownloadCacheHit(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "cached.mmdb")
	if err := os.WriteFile(dest, []byte("already here"), 0o644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// a base that would fail to resolve, proving no request is made on a
	// cache hit.
	got, err := download(context.Background(), "http://invalid.invalid/%s.gz", "cached.mmdb", dir)
	if err != nil {
		t.Fatalf("download() with a warm cache = %v, want nil error", err)
	}
	if got != dest {
		t.Errorf("download() = %q, want %q", got, dest)
	}
}

func TestDownloadCurrentMonthSuccess(t *testing.T) {
	cur, _ := currentAndPrevYM()
	body := gzipBytes(t, "mmdb bytes for "+cur)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dl-"+cur+".gz" {
			t.Errorf("unexpected request path %s, want the current month only", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest, err := download(context.Background(), srv.URL+"/dl-%s.gz", "out.mmdb", dir)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "mmdb bytes for "+cur {
		t.Errorf("downloaded content = %q, want the current month's decompressed body", got)
	}
}

func TestDownloadFallsBackToPreviousMonth(t *testing.T) {
	cur, prev := currentAndPrevYM()
	body := gzipBytes(t, "mmdb bytes for "+prev)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl-" + cur + ".gz":
			w.WriteHeader(http.StatusNotFound)
		case "/dl-" + prev + ".gz":
			w.WriteHeader(http.StatusOK)
			w.Write(body)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest, err := download(context.Background(), srv.URL+"/dl-%s.gz", "out.mmdb", dir)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "mmdb bytes for "+prev {
		t.Errorf("downloaded content = %q, want the fallback previous month's body", got)
	}
}

func TestDownloadBothMonthsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := download(context.Background(), srv.URL+"/dl-%s.gz", "missing.mmdb", dir)
	if err == nil {
		t.Fatal("download() with both months failing = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "missing.mmdb") {
		t.Errorf("error = %q, want it to name the dest file", err)
	}
}

func TestFetchGzipRejectsFakeSuccess(t *testing.T) {
	// a 200 whose body isn't actually gzip (e.g. an html error page served
	// with status 200 by some cdns) must not be trusted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>not gzip</html>"))
	}))
	defer srv.Close()

	if _, err := fetchGzip(context.Background(), srv.URL); err == nil {
		t.Error("fetchGzip on a non-gzip 200 body = nil error, want an error")
	}
}

func TestDownloadASNCategoriesCacheHit(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "ipverse-as-categories.json")
	seed := `[{"asn":1,"metadata":{"category":"isp"}},{"asn":2,"metadata":{}}]`
	if err := os.WriteFile(dest, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// no server at all: a cache hit must not make any request.
	got, err := DownloadASNCategories(context.Background(), dir)
	if err != nil {
		t.Fatalf("DownloadASNCategories with a warm cache = %v, want nil error", err)
	}
	want := map[int]string{1: "isp"}
	if len(got) != len(want) || got[1] != "isp" {
		t.Errorf("categories = %v, want %v", got, want)
	}
}

func TestDownloadASNCategoriesStreamDecode(t *testing.T) {
	entries := []map[string]any{
		{"asn": 1, "metadata": map[string]any{"category": "isp"}},
		{"asn": 2, "metadata": map[string]any{"category": ""}},
		{"asn": 3, "metadata": map[string]any{}},
		{"asn": 4, "metadata": map[string]any{"category": "business"}},
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(raw)
	}))
	defer srv.Close()

	dir := t.TempDir()
	orig := asnMetaURL
	asnMetaURL = srv.URL
	defer func() { asnMetaURL = orig }()

	got, err := DownloadASNCategories(context.Background(), dir)
	if err != nil {
		t.Fatalf("DownloadASNCategories: %v", err)
	}
	want := map[int]string{1: "isp", 4: "business"}
	if len(got) != len(want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("categories[%d] = %q, want %q", k, got[k], v)
		}
	}
}

func TestDownloadASNCategoriesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	orig := asnMetaURL
	asnMetaURL = srv.URL
	defer func() { asnMetaURL = orig }()

	if _, err := DownloadASNCategories(context.Background(), dir); err == nil {
		t.Error("DownloadASNCategories against a 500 = nil error, want an error")
	}
}
