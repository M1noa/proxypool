package egress

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "proxies.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPicksFastestHTTP(t *testing.T) {
	p := write(t, `[
	  {"ip": "9.9.9.9", "port": 1, "protocols": ["http"], "response_time_ms": 500},
	  {"ip": "8.8.8.8", "port": 2, "protocols": ["socks5"], "response_time_ms": 10},
	  {"ip": "7.7.7.7", "port": 3, "protocols": ["http"], "response_time_ms": null},
	  {"ip": "1.1.1.1", "port": 4, "protocols": ["http", "https"], "response_time_ms": 50}
	]`)
	eg := Load(p)
	if eg == nil {
		t.Fatal("expected a pool")
	}
	if eg.Len() != 2 {
		t.Fatalf("len = %d, want 2 (socks-only and unmeasured dropped)", eg.Len())
	}
	// fastest first: 1.1.1.1:4, then 9.9.9.9:1
	if got := eg.Next().String(); got != "http://1.1.1.1:4" {
		t.Errorf("first = %q, want fastest", got)
	}
	if got := eg.Next().String(); got != "http://9.9.9.9:1" {
		t.Errorf("second = %q", got)
	}
	if got := eg.Next().String(); got != "http://1.1.1.1:4" {
		t.Errorf("third = %q, want rotation", got)
	}
}

func TestLoadMissingIsNil(t *testing.T) {
	if Load(filepath.Join(t.TempDir(), "nope.json")) != nil {
		t.Error("missing file should give nil pool")
	}
	var nilPool *Pool
	if nilPool.Next() != nil || nilPool.Len() != 0 {
		t.Error("nil pool should behave as empty")
	}
}
