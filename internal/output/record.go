// package output ports the tail of fetch_proxies.py: sort_records,
// write_outputs' json.dumps, and its README regeneration.
package output

import (
	"strconv"

	"github.com/M1noa/proxypool/internal/extract"
)

// CheckFields is the six history-derived keys write_outputs only emits when
// checking happened this run - either fresh (main's history phase) or
// inherited from a carried-over record that already had them from a prior
// run's json. A nil *CheckFields on a Record means those keys are absent
// entirely, not null.
type CheckFields struct {
	Reliability float64
	Quality     float64
	ChecksTotal int
	ChecksOK    int
	FirstSeen   *string
	LastSeen    *string
}

// Record is one write_outputs record: extract.Record plus whatever geoip/asn
// and history filled in, holding exactly the fields that reach json, in
// their fixed serialization order (see json.go).
type Record struct {
	IP                string
	IPVersion         string
	Port              int
	Protocols         []string
	Country           string
	Anonymity         string
	HTTPS             bool
	Sources           []string
	SourceMeta        map[string]any
	LastChecked       *string
	ResponseTimeMS    *int
	ResponseTimeRawMS *int
	ASN               *int
	AsOrg             string
	IPType            string
	Check             *CheckFields

	// Carried marks a record recycled from a prior run's json rather than
	// freshly fetched this run - write_outputs' README sources table skips
	// these (`if id(r) in carried: continue`) since they aren't fresh source
	// output. Not serialized to json.
	Carried bool
}

// FromRecord assembles an output Record from an extract.Record, matching
// write_outputs' setdefault defaults for asn/as_org/ip_type when geoip/asn
// lookups didn't run.
func FromRecord(r *extract.Record) *Record {
	return &Record{
		IP:                r.IP,
		IPVersion:         r.IPVersion,
		Port:              r.Port,
		Protocols:         r.Protocols,
		Country:           r.Country,
		Anonymity:         r.Anonymity,
		HTTPS:             r.HTTPS,
		Sources:           r.Sources,
		SourceMeta:        r.SourceMeta,
		LastChecked:       r.LastChecked,
		ResponseTimeMS:    r.ResponseTimeMS,
		ResponseTimeRawMS: r.ResponseTimeRawMS,
	}
}

// Key is ip:port, the same identity extract.Record.Key uses.
func (r *Record) Key() string {
	return r.IP + ":" + strconv.Itoa(r.Port)
}
