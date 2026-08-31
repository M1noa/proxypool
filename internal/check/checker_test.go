package check

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/M1noa/proxypool/internal/extract"
)

// probePlan, classifyAnon, and the pure helpers below have no network
// dependency and get real unit tests. calibrate/probe/checkOne hit hardcoded
// upstream urls (google.com, azenv.net, speed.cloudflare.com) with no
// injection point, same as python's lib/check.py — which has no unit test
// suite either — so those stay covered only by the cli-level differential
// harness the plan's build-order step 6 gate calls for.

func rec(protocols ...string) *extract.Record {
	return &extract.Record{IP: "1.2.3.4", Port: 8080, Protocols: protocols, Provided: map[string]bool{}}
}

func TestProbePlan(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty claims https+http pair", nil, []string{"https", "http"}},
		{"unrecognized claim treated as unknown", []string{"ftp"}, []string{"https", "http"}},
		{"http claimed", []string{"http"}, []string{"https", "http"}},
		{"https claimed", []string{"https"}, []string{"https", "http"}},
		{"socks4 only, no discovery pair", []string{"socks4"}, []string{"socks4"}},
		{"socks5 only, no discovery pair", []string{"socks5"}, []string{"socks5"}},
		{"socks4 and socks5, no discovery pair", []string{"socks4", "socks5"}, []string{"socks4", "socks5"}},
		{"socks4 plus http adds discovery pair", []string{"socks4", "http"}, []string{"socks4", "https", "http"}},
		{"socks5 plus https adds discovery pair", []string{"socks5", "https"}, []string{"socks5", "https", "http"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := probePlan(rec(c.in...))
			if len(got) != len(c.want) {
				t.Fatalf("probePlan(%v) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("probePlan(%v) = %v, want %v", c.in, got, c.want)
				}
			}
		})
	}
}

func TestClassifyAnon(t *testing.T) {
	cases := []struct {
		name string
		text string
		myIP string
		want string
	}{
		{"my ip present is transparent", "your ip is 5.6.7.8, hello", "5.6.7.8", "transparent"},
		{"my ip check is case sensitive, no match on case folding", "IP=5.6.7.8", "5.6.7.8", "transparent"},
		{"via header name marks anonymous", "VIA: 1.1 proxy", "9.9.9.9", "anonymous"},
		{"x-forwarded-for marks anonymous", "X-Forwarded-For: 2.2.2.2", "9.9.9.9", "anonymous"},
		{"forwarded marks anonymous", "Forwarded: for=2.2.2.2", "9.9.9.9", "anonymous"},
		{"client-ip marks anonymous", "Client-IP: 2.2.2.2", "9.9.9.9", "anonymous"},
		{"neither present is elite", "nothing interesting here", "9.9.9.9", "elite"},
		{"empty myip skips transparent check", "5.6.7.8 shows up but myip is empty", "", "elite"},
		{"myip match beats header presence", "via 1.1 proxy, your ip 5.6.7.8", "5.6.7.8", "transparent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyAnon(c.text, c.myIP); got != c.want {
				t.Errorf("classifyAnon(%q, %q) = %q, want %q", c.text, c.myIP, got, c.want)
			}
		})
	}
}

func TestFetchOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("body"))
		case "/notfound":
			w.WriteHeader(http.StatusNotFound)
		case "/servererror":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	client := &http.Client{}
	ctx := context.Background()

	body, err := fetchOK(ctx, client, srv.URL+"/ok")
	if err != nil || body != "body" {
		t.Fatalf("fetchOK(/ok) = %q, %v, want %q, nil", body, err, "body")
	}
	if _, err := fetchOK(ctx, client, srv.URL+"/notfound"); err == nil {
		t.Error("fetchOK(/notfound) = nil error, want raise_for_status equivalent")
	}
	if _, err := fetchOK(ctx, client, srv.URL+"/servererror"); err == nil {
		t.Error("fetchOK(/servererror) = nil error, want raise_for_status equivalent")
	}
}

func TestTCPOpen(t *testing.T) {
	c := &Checker{}
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	defer ln.Close()

	if !c.tcpOpen(ctx, "127.0.0.1", addr.Port) {
		t.Error("tcpOpen against a listening port = false, want true")
	}

	// closed listener: the port stops accepting and connections refuse fast,
	// well inside timeoutDefault.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedAddr := closedLn.Addr().(*net.TCPAddr)
	closedLn.Close()

	if c.tcpOpen(ctx, "127.0.0.1", closedAddr.Port) {
		t.Error("tcpOpen against a closed port = true, want false")
	}
}

func TestTCPOpenCanceledContext(t *testing.T) {
	c := &Checker{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.tcpOpen(ctx, "127.0.0.1", 1) {
		t.Error("tcpOpen with an already-canceled context = true, want false")
	}
}

func TestTCPOpenRespectsTimeoutDefault(t *testing.T) {
	// shrink the dial timeout so a non-routable address fails fast instead of
	// waiting out the real 5s default.
	orig := timeoutDefault
	timeoutDefault = 200 * time.Millisecond
	defer func() { timeoutDefault = orig }()

	c := &Checker{}
	t0 := time.Now()
	// TEST-NET-2, guaranteed unroutable and non-refusing.
	if c.tcpOpen(context.Background(), "198.51.100.1", 9) {
		t.Error("tcpOpen against a blackhole address = true, want false")
	}
	if d := time.Since(t0); d > time.Second {
		t.Errorf("tcpOpen took %v, want it bounded by the shrunk timeoutDefault", d)
	}
}
