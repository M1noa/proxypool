package geoip

import "testing"

// Country and Lookup against real mmdb data have no injection point and no
// bundled test fixture (the upstream module doesn't ship one), same
// boundary call as internal/check's calibrate/probe: only the network-free
// paths — bad-input handling here — get unit tests.

func TestGeoIPCountryInvalidIP(t *testing.T) {
	g := &GeoIP{} // nil reader: only reachable if the bad-ip check runs first
	if got := g.Country("not-an-ip"); got != "" {
		t.Errorf("Country(%q) = %q, want \"\"", "not-an-ip", got)
	}
}

func TestAsnDBLookupInvalidIP(t *testing.T) {
	a := &AsnDB{} // nil reader and nil categories: same early-return contract
	got := a.Lookup("not-an-ip")
	if got.ASN != nil || got.AsOrg != "" || got.IPType != "" {
		t.Errorf("Lookup(%q) = %+v, want the zero value", "not-an-ip", got)
	}
}
