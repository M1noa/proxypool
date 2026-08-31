package output

import (
	"testing"

	"github.com/M1noa/proxypool/internal/extract"
)

func TestFromRecordCopiesFieldsAndLeavesGeoipAsnCheckZero(t *testing.T) {
	src := &extract.Record{
		IP: "1.2.3.4", IPVersion: "4", Port: 8080,
		Protocols: []string{"http"}, Country: "US", Anonymity: "elite",
		HTTPS: true, Sources: []string{"src1"},
		SourceMeta: map[string]any{"k": "v"},
	}
	r := FromRecord(src)
	if r.IP != src.IP || r.Port != src.Port || r.Country != src.Country {
		t.Errorf("FromRecord did not copy base fields: %+v", r)
	}
	if r.ASN != nil || r.AsOrg != "" || r.IPType != "" {
		t.Errorf("FromRecord must leave geoip/asn fields zero for the caller to fill in, got asn=%v as_org=%q ip_type=%q", r.ASN, r.AsOrg, r.IPType)
	}
	if r.Check != nil {
		t.Errorf("FromRecord must leave Check nil for the caller to fill in, got %+v", r.Check)
	}
}

func TestRecordKey(t *testing.T) {
	r := &Record{IP: "1.2.3.4", Port: 8080}
	if got, want := r.Key(), "1.2.3.4:8080"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
