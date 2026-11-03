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

// loadTest starts a server serving both feeds and loads them into a temp cache.
func loadTest(t *testing.T) *Feed {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ipsum.txt":
			w.Write([]byte(testIpsum))
		case "/fraud.csv":
			w.Write([]byte(testFraud))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	oldIpsum, oldFraud := ipsumURL, fraudURL
	ipsumURL, fraudURL = srv.URL+"/ipsum.txt", srv.URL+"/fraud.csv"
	t.Cleanup(func() { ipsumURL, fraudURL = oldIpsum, oldFraud })

	f, err := Load(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestLoadMergesBothFeeds(t *testing.T) {
	f := loadTest(t)

	if ipsum, fraud := f.Len(); ipsum != 3 || fraud != 4 {
		t.Errorf("Len() = %d, %d; want 3, 4", ipsum, fraud)
	}

	e, ok := f.Entry("1.2.3.4")
	if !ok {
		t.Fatal("1.2.3.4 not listed")
	}
	want := Entry{Blacklists: 7, FraudScore: 95, FraudReports: 33, FraudCategory: "c2", FraudType: "proxy"}
	if e != want {
		t.Errorf("1.2.3.4 = %+v, want %+v", e, want)
	}

	// ipsum only: the fraud fields stay zero, and the empty type is empty.
	e, ok = f.Entry("5.6.7.8")
	if !ok {
		t.Fatal("5.6.7.8 not listed")
	}
	if e.Blacklists != 2 || e.FraudScore != 60 || e.FraudCategory != "scanner" || e.FraudType != "" {
		t.Errorf("5.6.7.8 = %+v", e)
	}

	e, ok = f.Entry("9.9.9.9")
	if !ok || e.Blacklists != 0 || e.FraudType != "vpn" {
		t.Errorf("9.9.9.9 = %+v, ok=%v", e, ok)
	}

	if _, ok := f.Entry("8.8.8.8"); ok {
		t.Error("8.8.8.8 listed but is in neither feed")
	}
	if _, ok := f.Entry("not-an-ip"); ok {
		t.Error("garbage ip parsed")
	}
}

func TestIPv6(t *testing.T) {
	f := loadTest(t)
	e, ok := f.Entry("2001:db8::1")
	if !ok {
		t.Fatal("2001:db8::1 not listed")
	}
	if e.Blacklists != 4 || e.FraudScore != 80 || e.FraudReports != 7 || e.FraudType != "tor" {
		t.Errorf("2001:db8::1 = %+v", e)
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
	// listCap blacklists saturates the blacklist signal on its own
	if got := (Entry{Blacklists: 6}).QualityFactor(); math.Abs(got-(1-maxPenalty)) > 1e-9 {
		t.Errorf("6 blacklists factor = %v, want %v", got, 1-maxPenalty)
	}
	// worst case is bounded, never negative
	if got := (Entry{Blacklists: 10, FraudScore: 100}).QualityFactor(); math.Abs(got-(1-maxPenalty)) > 1e-9 {
		t.Errorf("worst case factor = %v, want %v", got, 1-maxPenalty)
	}
	for _, e := range []Entry{
		{Blacklists: 3, FraudScore: 70},
		{Blacklists: 9},
		{FraudScore: 100},
	} {
		if f := e.QualityFactor(); f < 1-maxPenalty || f > 1 {
			t.Errorf("%+v factor = %v out of range", e, f)
		}
	}
}

func TestFallsBackToCachedCopy(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ipsum.txt":
			w.Write([]byte(testIpsum))
		case "/fraud.csv":
			w.Write([]byte(testFraud))
		}
	}))
	oldIpsum, oldFraud := ipsumURL, fraudURL
	ipsumURL, fraudURL = srv.URL+"/ipsum.txt", srv.URL+"/fraud.csv"
	if _, err := Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv.Close()

	// upstream gone, cache present: still loads.
	f, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load from cache: %v", err)
	}
	if _, ok := f.Entry("1.2.3.4"); !ok {
		t.Error("cached feed lost 1.2.3.4")
	}
	t.Cleanup(func() { ipsumURL, fraudURL = oldIpsum, oldFraud })
}
