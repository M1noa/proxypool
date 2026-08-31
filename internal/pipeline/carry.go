package pipeline

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/output"
)

// item is one record in flight: the extract.Record the fetch and check layers
// operate on, plus the output-only fields geoip, asn and history fill in after.
type item struct {
	rec     *extract.Record
	carried bool
	asn     *int
	asOrg   string
	ipType  string
	check   *output.CheckFields
}

// prevRecord is one entry of a previous run's proxies.json. the schema is
// closed - this program wrote the file - so the 15 always-present keys plus the
// 6 the checker phase adds cover it exactly.
type prevRecord struct {
	IP                string         `json:"ip"`
	IPVersion         string         `json:"ip_version"`
	Port              int            `json:"port"`
	Protocols         []string       `json:"protocols"`
	Country           string         `json:"country"`
	Anonymity         string         `json:"anonymity"`
	HTTPS             bool           `json:"https"`
	Sources           []string       `json:"sources"`
	SourceMeta        map[string]any `json:"source_meta"`
	LastChecked       *string        `json:"last_checked"`
	ResponseTimeMS    *int           `json:"response_time_ms"`
	ResponseTimeRawMS *int           `json:"response_time_raw_ms"`
	ASN               *int           `json:"asn"`
	AsOrg             string         `json:"as_org"`
	IPType            string         `json:"ip_type"`

	Reliability *float64 `json:"reliability"`
	Quality     *float64 `json:"quality"`
	ChecksTotal int      `json:"checks_total"`
	ChecksOK    int      `json:"checks_ok"`
	FirstSeen   *string  `json:"first_seen"`
	LastSeen    *string  `json:"last_seen"`
}

// loadPrevious is main()'s carry-over block: last run's proxies get re-checked
// alongside this run's, so one no source happened to republish this hour is not
// dropped on the spot. entries already in seen are skipped. existed reports
// whether the file was there at all, which is what python gates its log line on.
func loadPrevious(path string, seen map[string]bool) (items []*item, existed bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // source_meta values round-trip verbatim
	var rows []prevRecord
	if err := dec.Decode(&rows); err != nil {
		return nil, true, err
	}

	for i := range rows {
		p := &rows[i]
		rec := &extract.Record{
			IP:                p.IP,
			IPVersion:         p.IPVersion,
			Port:              p.Port,
			Protocols:         p.Protocols,
			Country:           p.Country,
			Anonymity:         p.Anonymity,
			HTTPS:             p.HTTPS,
			Sources:           p.Sources,
			SourceMeta:        p.SourceMeta,
			Provided:          map[string]bool{}, // _provided is popped, so nothing is vouched for
			LastChecked:       p.LastChecked,
			ResponseTimeMS:    p.ResponseTimeMS,
			ResponseTimeRawMS: p.ResponseTimeRawMS,
		}
		if seen[rec.Key()] {
			continue
		}
		if rec.Protocols == nil {
			rec.Protocols = []string{}
		}
		if rec.SourceMeta == nil {
			rec.SourceMeta = map[string]any{}
		}
		it := &item{rec: rec, carried: true, asn: p.ASN, asOrg: p.AsOrg, ipType: p.IPType}
		// the 6 score keys travel together: present only if the run that wrote
		// them checked. -skip-check preserves them, otherwise they get replaced.
		if p.Reliability != nil {
			it.check = &output.CheckFields{
				Reliability: *p.Reliability,
				Quality:     deref(p.Quality),
				ChecksTotal: p.ChecksTotal,
				ChecksOK:    p.ChecksOK,
				FirstSeen:   p.FirstSeen,
				LastSeen:    p.LastSeen,
			}
		}
		items = append(items, it)
	}
	return items, true, nil
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
