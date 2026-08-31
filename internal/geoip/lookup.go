package geoip

import (
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// GeoIP wraps the country-lite mmdb. GeoIP.Country is country().
type GeoIP struct {
	reader *maxminddb.Reader
}

// OpenGeoIP memory-maps path; the reader is safe for concurrent lookups.
func OpenGeoIP(path string) (*GeoIP, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &GeoIP{reader: r}, nil
}

func (g *GeoIP) Close() error { return g.reader.Close() }

// Country returns the upper-cased 2-letter iso code, or "" on any failure —
// bad ip, no match, or a code that isn't exactly 2 characters. python
// swallows every exception from reader.get(ip) the same way.
func (g *GeoIP) Country(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	res := g.reader.Lookup(addr)
	if err := res.Decode(&rec); err != nil || !res.Found() {
		return ""
	}
	if len(rec.Country.ISOCode) != 2 {
		return ""
	}
	return strings.ToUpper(rec.Country.ISOCode)
}

// AsnInfo is AsnDB.lookup's return dict: ASN nil and AsOrg/IPType "" are the
// python None/"" defaults for a miss or a lookup failure.
type AsnInfo struct {
	ASN    *int
	AsOrg  string
	IPType string
}

// AsnDB wraps the asn-lite mmdb plus the ipverse category map.
type AsnDB struct {
	reader     *maxminddb.Reader
	categories map[int]string
}

// OpenAsnDB memory-maps path and pairs it with categories from
// DownloadASNCategories.
func OpenAsnDB(path string, categories map[int]string) (*AsnDB, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &AsnDB{reader: r, categories: categories}, nil
}

func (a *AsnDB) Close() error { return a.reader.Close() }

// Lookup is AsnDB.lookup.
func (a *AsnDB) Lookup(ip string) AsnInfo {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return AsnInfo{}
	}
	var rec struct {
		ASN   uint32 `maxminddb:"autonomous_system_number"`
		AsOrg string `maxminddb:"autonomous_system_organization"`
	}
	res := a.reader.Lookup(addr)
	if err := res.Decode(&rec); err != nil || !res.Found() {
		return AsnInfo{}
	}
	info := AsnInfo{AsOrg: rec.AsOrg}
	// as 0 is reserved and never assigned in db-ip's dataset, so treating a
	// zero value as "field absent" matches python's isinstance(n, int) check
	// on a dict.get() that returned None without needing a pointer decode.
	if rec.ASN != 0 {
		n := int(rec.ASN)
		info.ASN = &n
		info.IPType = a.categories[n]
	}
	return info
}
