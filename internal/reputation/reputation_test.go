package reputation

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testIpsum = `# IPsum Threat Intelligence Feed
# (https://github.com/stamparm/ipsum)
#
# IP	number of (black)lists
1.2.3.4	7
5.6.7.8	2
2001:db8::1	4
`

const testFraud = `# ffraud confirmed-abusive IPs
ip,ffraud_score,confirmations,category,type
1.2.3.4,95,33,c2,proxy
5.6.7.8,60,2,scanner,
2001:db8::1,80,7,botnet,tor
9.9.9.9,55,3,web_attack,vpn
`

// the drop ranges are deliberately routed away from the ipsum/ffraud addresses
// so each assertion above stays about the map it names.
const testDrop = `; Spamhaus DROP List
203.0.113.0/24 ; SBL1
198.51.100.0/24 ; SBL2
`

const testDrop6 = `; Spamhaus IPv6 DROP List
2001:db8:dead::/48 ; SBL3
`

// asndrop is newline-delimited json and ends with a metadata object, exactly as
// the real file does; the last line must not be read as an asn.
const testASN = `{"asn":64500,"rir":"arin","asname":"TEST-AS"}
{"asn":64501,"rir":"arin","asname":"TEST-AS2"}
{"type":"metadata","timestamp":1794778004,"records":2}
`

// testTor is the plain address list shape; testVPN is netblocks; testAbuse
// mirrors the annotated abuseipdb export, a bare address and a netblock with a
// trailing "# CC ASxxxx org" comment.
const testTor = `171.25.193.25
109.70.100.4
`

const testVPN = `198.18.0.0/15
`

const testAbuse = `# Aggregated Blocklist for AbuseIPDB
192.0.2.0/25  # NL  AS64500   EXAMPLE BV
192.0.2.200  # US  AS64501   EXAMPLE NET
`

// testFeeds is every url Load reads, so a test that forgets one fails loudly
// instead of reaching the real upstream.
var testFeeds = map[string]string{
	"/ipsum.txt":    testIpsum,
	"/fraud.csv":    testFraud,
	"/drop.txt":     testDrop,
	"/dropv6.txt":   testDrop6,
	"/asndrop.json": testASN,
	"/tor.txt":      testTor,
	"/vpn.txt":      testVPN,
	"/abuse.txt":    testAbuse,
}

// swapURLs points the package vars at srv and returns the restore func.
func swapURLs(srv *httptest.Server) func() {
	oldIpsum, oldFraud := ipsumURL, fraudURL
	oldDrop, oldDrop6, oldASN := dropURL, dropv6URL, asnDropURL
	oldTor, oldVPN, oldAbuse := torURL, vpnURL, abuseURL
	ipsumURL, fraudURL = srv.URL+"/ipsum.txt", srv.URL+"/fraud.csv"
	dropURL, dropv6URL = srv.URL+"/drop.txt", srv.URL+"/dropv6.txt"
	asnDropURL = srv.URL + "/asndrop.json"
	torURL, vpnURL, abuseURL = srv.URL+"/tor.txt", srv.URL+"/vpn.txt", srv.URL+"/abuse.txt"
	return func() {
		ipsumURL, fraudURL = oldIpsum, oldFraud
		dropURL, dropv6URL, asnDropURL = oldDrop, oldDrop6, oldASN
		torURL, vpnURL, abuseURL = oldTor, oldVPN, oldAbuse
	}
}

func feedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := testFeeds[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// loadTest starts a server serving every feed and loads them into a temp cache.
func loadTest(t *testing.T) *Feed {
	t.Helper()
	srv := feedServer(t)
	t.Cleanup(swapURLs(srv))

	f, err := Load(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestLoadMergesAllFeeds(t *testing.T) {
	f := loadTest(t)

	if c := f.Counts(); c != (Counts{Ipsum: 3, Fraud: 4, Drop: 3, ASN: 2, Tor: 2, VPN: 1, Abuse: 2}) {
		t.Errorf("Counts() = %+v; want 3, 4, 3, 2, 2, 1, 2", c)
	}

	e, ok := f.Entry("1.2.3.4", 0)
	if !ok {
		t.Fatal("1.2.3.4 not listed")
	}
	want := Entry{Blacklists: 7, FraudScore: 95, FraudReports: 33, FraudCategory: "c2", FraudType: "proxy"}
	if e != want {
		t.Errorf("1.2.3.4 = %+v, want %+v", e, want)
	}

	// ipsum only: the fraud fields stay zero, and the empty type is empty.
	e, ok = f.Entry("5.6.7.8", 0)
	if !ok {
		t.Fatal("5.6.7.8 not listed")
	}
	if e.Blacklists != 2 || e.FraudScore != 60 || e.FraudCategory != "scanner" || e.FraudType != "" {
		t.Errorf("5.6.7.8 = %+v", e)
	}

	e, ok = f.Entry("9.9.9.9", 0)
	if !ok || e.Blacklists != 0 || e.FraudType != "vpn" {
		t.Errorf("9.9.9.9 = %+v, ok=%v", e, ok)
	}

	if _, ok := f.Entry("8.8.8.8", 0); ok {
		t.Error("8.8.8.8 listed but is in no feed")
	}
	if _, ok := f.Entry("not-an-ip", 0); ok {
		t.Error("garbage ip parsed")
	}
}

func TestIPv6(t *testing.T) {
	f := loadTest(t)
	e, ok := f.Entry("2001:db8::1", 0)
	if !ok {
		t.Fatal("2001:db8::1 not listed")
	}
	if e.Blacklists != 4 || e.FraudScore != 80 || e.FraudReports != 7 || e.FraudType != "tor" {
		t.Errorf("2001:db8::1 = %+v", e)
	}
}

func TestSpamhausNetblocksAndASN(t *testing.T) {
	f := loadTest(t)

	// a drop netblock on its own lists an address no other feed knows
	e, ok := f.Entry("203.0.113.7", 0)
	if !ok || !e.SpamhausDrop || e.SpamhausASN {
		t.Fatalf("203.0.113.7 = %+v, ok=%v", e, ok)
	}
	// just outside the /24
	if _, ok := f.Entry("203.0.114.7", 0); ok {
		t.Error("203.0.114.7 matched a drop range it is not in")
	}
	// the ipv6 list
	if e, ok := f.Entry("2001:db8:dead::5", 0); !ok || !e.SpamhausDrop {
		t.Errorf("2001:db8:dead::5 = %+v, ok=%v", e, ok)
	}
	// asn-drop alone, no ip list
	e, ok = f.Entry("8.8.4.4", 64500)
	if !ok || !e.SpamhausASN || e.SpamhausDrop {
		t.Fatalf("8.8.4.4/64500 = %+v, ok=%v", e, ok)
	}
	// an asn that is not listed, on an address no other feed lists
	if _, ok := f.Entry("8.8.4.4", 64000); ok {
		t.Error("clean address with a clean asn reported")
	}
}

func TestTorVPNAndAbuseIPDB(t *testing.T) {
	f := loadTest(t)

	// an exit node and a vpn netblock list an address no other feed knows
	if e, ok := f.Entry("171.25.193.25", 0); !ok || !e.TorExit || e.VPN {
		t.Errorf("171.25.193.25 = %+v, ok=%v", e, ok)
	}
	if e, ok := f.Entry("198.19.7.7", 0); !ok || !e.VPN || e.TorExit {
		t.Errorf("198.19.7.7 = %+v, ok=%v", e, ok)
	}
	// the mirror mixes netblocks and bare addresses, both with annotations
	if e, ok := f.Entry("192.0.2.9", 0); !ok || !e.AbuseIPDB {
		t.Errorf("192.0.2.9 = %+v, ok=%v", e, ok)
	}
	if e, ok := f.Entry("192.0.2.200", 0); !ok || !e.AbuseIPDB {
		t.Errorf("192.0.2.200 = %+v, ok=%v", e, ok)
	}
	if _, ok := f.Entry("192.0.2.130", 0); ok {
		t.Error("192.0.2.130 outside the /25 and the bare row")
	}
	// an abuseipdb row alone saturates the aggregate signal
	if got := (Entry{AbuseIPDB: true}).Risk(); got != 1 {
		t.Errorf("abuseipdb risk = %v, want 1", got)
	}
	// tor and vpn say nothing about quality
	if got := (Entry{TorExit: true, VPN: true}).QualityFactor(); got != 1 {
		t.Errorf("tor/vpn factor = %v, want 1", got)
	}
}

func TestQualityFactorBounds(t *testing.T) {
	if got := (Entry{}).QualityFactor(); got != 1 {
		t.Errorf("unlisted factor = %v, want 1", got)
	}
	// one blacklist alone is a mild signal
	if got := (Entry{Blacklists: 1}).QualityFactor(); math.Abs(got-0.95) > 1e-9 {
		t.Errorf("1 blacklist factor = %v, want 0.95", got)
	}
	// listCap blacklists saturates the aggregate signal on its own
	if got := (Entry{Blacklists: 6}).QualityFactor(); math.Abs(got-(1-maxPenalty)) > 1e-9 {
		t.Errorf("6 blacklists factor = %v, want %v", got, 1-maxPenalty)
	}
	// a drop netblock alone is heavier than any aggregate signal can be, and
	// combines with one rather than replacing it
	drop := (Entry{SpamhausDrop: true}).QualityFactor()
	if math.Abs(drop-(1-netPenalty)) > 1e-9 {
		t.Errorf("drop-only factor = %v, want %v", drop, 1-netPenalty)
	}
	if drop >= 1-maxPenalty && maxPenalty > netPenalty {
		t.Errorf("drop weight %v is not heavier than aggregate weight %v", drop, 1-maxPenalty)
	}
	floor := (1 - maxPenalty) * (1 - netPenalty)
	if got := (Entry{Blacklists: 10, FraudScore: 100, SpamhausDrop: true}).QualityFactor(); math.Abs(got-floor) > 1e-9 {
		t.Errorf("worst case factor = %v, want %v", got, floor)
	}
	// a drop netblock is a stronger signal than an asn
	if d, a := (Entry{SpamhausDrop: true}).NetRisk(), (Entry{SpamhausASN: true}).NetRisk(); d <= a {
		t.Errorf("drop risk %v <= asn risk %v", d, a)
	}
	for _, e := range []Entry{
		{Blacklists: 3, FraudScore: 70},
		{Blacklists: 9},
		{FraudScore: 100},
		{SpamhausDrop: true},
		{SpamhausASN: true},
		{Blacklists: 2, FraudScore: 40, SpamhausDrop: true, SpamhausASN: true},
	} {
		if f := e.QualityFactor(); f < floor || f > 1 {
			t.Errorf("%+v factor = %v out of range", e, f)
		}
	}
}

func TestFallsBackToCachedCopy(t *testing.T) {
	dir := t.TempDir()

	srv := feedServer(t)
	t.Cleanup(swapURLs(srv))
	if _, err := Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv.Close()

	// upstream gone, cache present: still loads.
	f, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load from cache: %v", err)
	}
	if _, ok := f.Entry("1.2.3.4", 0); !ok {
		t.Error("cached feed lost 1.2.3.4")
	}
	if _, ok := f.Entry("203.0.113.7", 0); !ok {
		t.Error("cached feed lost the drop netblock")
	}
}
