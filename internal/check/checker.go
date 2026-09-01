// package check ports lib/check.py: verify a proxy's claimed protocols, time
// them, and classify anonymity. socks4.go hand-rolls the one protocol
// golang.org/x/net/proxy does not speak; pool.go and progress.go run the
// probes in bulk with a live progress line.
//
// python guards its network calls with _bounded()/PROBE_CAP/RECORD_CAP/
// CLOSE_CAP because asyncio.wait_for() awaits a cancelled task's teardown, so
// a coroutine wedged in a `finally` could hang the whole pool. context
// cancellation genuinely aborts a goroutine's work, and a leaked goroutine
// cannot block its caller, so none of that layer is ported.
package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/pyfmt"
)

const (
	checkURL      = "http://www.google.com/generate_204"
	checkURLHTTPS = "https://www.google.com/generate_204"
	speedtestURL  = "https://speed.cloudflare.com/__down?bytes=10000000"
	speedtestConn = 4
	baselinePings = 5

	concurrencyMin = 768
	concurrencyMax = 10000
)

// per-probe costs, measured against a real run: 2400 workers on 4 cpus and a
// 1123 mbps link sustained 590 probes/s at 6% cpu and ~35 mbps. each budget in
// deriveConcurrency is "how many probes fit in flight before this runs out".
const (
	probeSeconds   = 4.0       // mean occupancy: most probes are dead and time out
	probeCPUms     = 0.5       // tls handshake and teardown, per probe
	probeRAMBytes  = 256 << 10 // goroutine, its parallel transports, tls buffers
	probeWireBytes = 12 << 10  // handshake plus a 204, rounded up
	// checkOne probes a record's whole plan concurrently — at most socks4,
	// socks5, https, http — so a worker can hold four sockets at once.
	fdPerWorker = 6
	fdReserve   = 1024 // sources, duckdb, the mmdbs, stdio
)

// timeoutDefault and recordCap are vars, not consts, only so tests can shrink
// them instead of waiting out a real 5s/25s timeout.
var (
	timeoutDefault = 5 * time.Second
	// recordCap is a cheap backstop around one record's whole check (tcp open,
	// every probe, the anonymity echo): python's RECORD_CAP existed to force a
	// wedged coroutine's cancellation to actually take; context cancellation
	// here already does that, so this is just an upper bound on how long one
	// record may occupy a worker.
	recordCap = 25 * time.Second

	echoURLs = []string{"http://azenv.net/", "http://httpbin.org/get"}
	myIPURLs = []string{"https://api.ipify.org", "https://icanhazip.com"}
	anonKeys = []string{"via", "x-forwarded-for", "forwarded", "client-ip"}
)

// Checker holds the state one check_all run shares: the baseline latency to
// subtract from every probe, our own outbound ip for anonymity detection, the
// measured bandwidth, and the ip socks4 targets (it has no rdns, so the check
// endpoint must already be resolved).
type Checker struct {
	baseline float64 // ms, min of baselinePings direct pings
	myIP     string
	mbps     float64
	googleIP net.IP
	// timeout caps one probe. zero means timeoutDefault. calibration
	// deliberately ignores it: it measures our own link, not a proxy.
	timeout time.Duration
}

func (c *Checker) tmo() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return timeoutDefault
}

// probeOutcome is one protocol's result for one record: RT is the calibrated
// milliseconds on success, nil on failure.
type probeOutcome struct {
	Proto string
	RT    *int
}

// fetchOK does one GET and reads the whole body before returning, so a
// caller timing around the call is timing the same thing aiohttp's
// `await resp.text()` inside _fetch would. raises (returns an error) for any
// status >= 400, python's raise_for_status.
func fetchOK(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return string(body), nil
}

// calibrate pings the check endpoint directly (no proxy) baselinePings times
// and keeps the fastest, fills myIP from whichever MYIP url answers first,
// and resolves the check host once for socks4's benefit.
func (c *Checker) calibrate(ctx context.Context) error {
	direct := &http.Client{}
	var times []float64
	for i := 0; i < baselinePings; i++ {
		pctx, cancel := context.WithTimeout(ctx, timeoutDefault)
		t0 := time.Now()
		_, err := fetchOK(pctx, direct, checkURL)
		cancel()
		if err == nil {
			times = append(times, float64(time.Since(t0))/float64(time.Millisecond))
		}
	}
	if len(times) == 0 {
		return errors.New("cannot reach check endpoint for baseline")
	}
	c.baseline = slices.Min(times)

	for _, u := range myIPURLs {
		pctx, cancel := context.WithTimeout(ctx, timeoutDefault)
		body, err := fetchOK(pctx, direct, u)
		cancel()
		if err == nil {
			c.myIP = extract.PyStrip(body)
			break
		}
	}

	// socks4 has no rdns (aiohttp_socks defaults it off), so the destination
	// has to already be an ip. a lookup failure just leaves socks4 unusable
	// for this run's probes; it is not fatal to check_all.
	rctx, cancel := context.WithTimeout(ctx, timeoutDefault)
	defer cancel()
	if ips, err := net.DefaultResolver.LookupIP(rctx, "ip4", "www.google.com"); err == nil && len(ips) > 0 {
		c.googleIP = ips[0]
	}
	return nil
}

// measureMbps runs speedtestConn concurrent full downloads under a 30s cap
// and returns the aggregate throughput, or 0 on any failure.
func (c *Checker) measureMbps(ctx context.Context) float64 {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := &http.Client{}
	t0 := time.Now()
	var wg sync.WaitGroup
	var total int64
	var mu sync.Mutex
	failed := false

	for i := 0; i < speedtestConn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, speedtestURL, nil)
			if err != nil {
				mu.Lock()
				failed = true
				mu.Unlock()
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				mu.Lock()
				failed = true
				mu.Unlock()
				return
			}
			defer resp.Body.Close()
			n, err := io.Copy(io.Discard, resp.Body)
			mu.Lock()
			if err != nil {
				failed = true
			} else {
				total += n
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if failed || ctx.Err() != nil {
		return 0
	}
	elapsed := time.Since(t0).Seconds()
	if elapsed <= 0 {
		return 0
	}
	c.mbps = float64(total) * 8 / 1e6 / elapsed
	return c.mbps
}

// tcpOpen is _tcp_open: a bare connect-then-close reachability check.
func (c *Checker) tcpOpen(ctx context.Context, ip string, port int) bool {
	pctx, cancel := context.WithTimeout(ctx, c.tmo())
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(pctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// probe is _probe: one timed fetch through ip:port using proto, returning the
// raw elapsed milliseconds. despite _probe's python docstring, the value is
// not calibrated — checkOne subtracts the baseline itself.
func (c *Checker) probe(ctx context.Context, ip string, port int, proto string) (float64, error) {
	pctx, cancel := context.WithTimeout(ctx, c.tmo())
	defer cancel()

	proxyAddr := net.JoinHostPort(ip, strconv.Itoa(port))
	target := checkURL
	tr := &http.Transport{DisableKeepAlives: true}

	switch proto {
	case "https":
		target = checkURLHTTPS
		fallthrough
	case "http":
		tr.Proxy = http.ProxyURL(&neturl.URL{Scheme: "http", Host: proxyAddr})
	case "socks5":
		// x/net/proxy sends the hostname as-is, matching aiohttp_socks'
		// socks5 default of rdns=True
		d, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err != nil {
			return 0, err
		}
		tr.DialContext = func(_ context.Context, network, _ string) (net.Conn, error) {
			return d.Dial(network, "www.google.com:80")
		}
	case "socks4":
		if c.googleIP == nil {
			return 0, errors.New("socks4: check host did not resolve")
		}
		tr.DialContext = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			return dialSOCKS4(ctx, proxyAddr, c.googleIP, 80)
		}
	default:
		return 0, fmt.Errorf("unknown protocol %q", proto)
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	t0 := time.Now()
	if _, err := fetchOK(pctx, client, target); err != nil {
		return 0, err
	}
	return float64(time.Since(t0)) / float64(time.Millisecond), nil
}

// probePlan is _probe_plan: socks4/socks5 probe whenever claimed, plus an
// https+http pair whenever either is claimed or nothing recognizable was.
//
// four bools rather than a set, because this runs once per record and a run
// holds ~2m of them: a map here is a map allocation there.
func probePlan(r *extract.Record) []string {
	var s4, s5, hasHTTP, hasHTTPS bool
	for _, p := range r.Protocols {
		switch p {
		case "socks4":
			s4 = true
		case "socks5":
			s5 = true
		case "http":
			hasHTTP = true
		case "https":
			hasHTTPS = true
		}
	}
	plan := make([]string, 0, 4)
	if s4 {
		plan = append(plan, "socks4")
	}
	if s5 {
		plan = append(plan, "socks5")
	}
	// `!known` in python was !(s4||s5||http||https), and the two http terms are
	// already covered by the left of the ||
	if hasHTTP || hasHTTPS || !(s4 || s5) {
		plan = append(plan, "https", "http")
	}
	return plan
}

// classifyAnon is _classify_anon. myIP is matched case-sensitively against
// the raw echo body before the body is lowercased for the header-name test.
func classifyAnon(text, myIP string) string {
	if myIP != "" && strings.Contains(text, myIP) {
		return "transparent"
	}
	low := strings.ToLower(text)
	for _, k := range anonKeys {
		if strings.Contains(low, k) {
			return "anonymous"
		}
	}
	return "elite"
}

// echoAnonymity is _echo_anonymity: fetch each echo url through the proxy's
// http protocol until one succeeds, and classify that one. "" if all fail.
func echoAnonymity(ctx context.Context, ip string, port int, myIP string, timeout time.Duration) string {
	proxyAddr := net.JoinHostPort(ip, strconv.Itoa(port))
	tr := &http.Transport{
		DisableKeepAlives: true,
		Proxy:             http.ProxyURL(&neturl.URL{Scheme: "http", Host: proxyAddr}),
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	for _, u := range echoURLs {
		pctx, cancel := context.WithTimeout(ctx, timeout)
		text, err := fetchOK(pctx, client, u)
		cancel()
		if err == nil {
			return classifyAnon(text, myIP)
		}
	}
	return ""
}

// checkOne is _check_one: verify every probe in plan, keep the fastest
// calibrated result, fold https success into http, and fill in anonymity
// when nothing already provided it. it mutates r in place and reports
// whether anything in plan came back alive.
func (c *Checker) checkOne(ctx context.Context, r *extract.Record, plan []string) (bool, []probeOutcome) {
	outcomes := make([]probeOutcome, len(plan))
	for i, p := range plan {
		outcomes[i] = probeOutcome{Proto: p}
	}
	if len(plan) == 0 || !c.tcpOpen(ctx, r.IP, r.Port) {
		return false, outcomes
	}

	type result struct {
		ms  float64
		err error
	}
	results := make([]result, len(plan))
	var wg sync.WaitGroup
	for i, proto := range plan {
		wg.Add(1)
		go func(i int, proto string) {
			defer wg.Done()
			ms, err := c.probe(ctx, r.IP, r.Port, proto)
			results[i] = result{ms: ms, err: err}
		}(i, proto)
	}
	wg.Wait()

	var okHTTP, okHTTPS, okS4, okS5, any bool
	var bestRT, bestRaw int
	haveBest := false
	for i, res := range results {
		if res.err != nil {
			continue
		}
		raw := pyfmt.Round(res.ms)
		calibrated := max(1, pyfmt.Round(res.ms-c.baseline))
		outcomes[i].RT = &calibrated
		switch plan[i] {
		case "http":
			okHTTP = true
		case "https":
			okHTTPS = true
		case "socks4":
			okS4 = true
		case "socks5":
			okS5 = true
		}
		any = true
		if !haveBest || calibrated < bestRT {
			bestRT, bestRaw, haveBest = calibrated, raw, true
		}
	}
	if !any {
		return false, outcomes
	}
	if okHTTPS {
		okHTTP = true
	}
	// appended in the order python's sorted() would have produced, so no sort
	protos := make([]string, 0, 4)
	if okHTTP {
		protos = append(protos, "http")
	}
	if okHTTPS {
		protos = append(protos, "https")
	}
	if okS4 {
		protos = append(protos, "socks4")
	}
	if okS5 {
		protos = append(protos, "socks5")
	}

	r.Protocols = protos
	r.ResponseTimeMS = &bestRT
	r.ResponseTimeRawMS = &bestRaw
	if okHTTPS {
		r.HTTPS = true
	}
	if r.Anonymity == "" && !r.Provided["anonymity"] && okHTTP {
		r.Anonymity = echoAnonymity(ctx, r.IP, r.Port, c.myIP, c.tmo())
	}
	return true, outcomes
}
