// package pipeline ports fetch_proxies.py main(): fetch every source, merge by
// ip:port, fill in geoip/asn, probe what's alive, score it against history,
// and write the outputs.
package pipeline

import (
	"sort"

	"github.com/M1noa/proxypool/internal/extract"
	"github.com/M1noa/proxypool/internal/geoip"
)

// anonRank is ANON_RANK. extract.NormRecord only ever emits these four values,
// so python's KeyError on an unknown one is unreachable here.
var anonRank = map[string]int{"": 0, "transparent": 1, "anonymous": 2, "elite": 3}

// merge is merge(): one record per ip:port, unioning the list fields and
// keeping the strongest scalar across every source that reported it. first-seen
// order is preserved, which sort_records' stable sort then rides on.
func merge(all []*extract.Record) []*extract.Record {
	out := make([]*extract.Record, 0, len(all))
	seen := make(map[string]*extract.Record, len(all))
	for _, rec := range all {
		cur, ok := seen[rec.Key()]
		if !ok {
			seen[rec.Key()] = rec
			out = append(out, rec)
			continue
		}
		cur.Sources = sortedUnion(cur.Sources, rec.Sources)
		cur.Protocols = sortedUnion(cur.Protocols, rec.Protocols)
		for f := range rec.Provided {
			cur.Provided[f] = true
		}
		if cur.Country == "" {
			cur.Country = rec.Country
		}
		if cur.CountryName == "" {
			cur.CountryName = rec.CountryName
		}
		if anonRank[rec.Anonymity] > anonRank[cur.Anonymity] {
			cur.Anonymity = rec.Anonymity
		}
		cur.HTTPS = cur.HTTPS || rec.HTTPS
		// own-check values overwrite later; here the fastest report wins
		if rec.ResponseTime != nil && (cur.ResponseTime == nil || *rec.ResponseTime < *cur.ResponseTime) {
			cur.ResponseTime = rec.ResponseTime
		}
		for k, v := range rec.SourceMeta {
			if _, ok := cur.SourceMeta[k]; !ok {
				cur.SourceMeta[k] = v
			}
		}
	}
	for _, r := range out {
		r.LastChecked = nil // filled by the checker phase
	}
	return out
}

// finalize is finalize(): resolve country from country_name where a source gave
// a name but no code, then move the source-reported response time onto the
// integer field the checker phase overwrites.
func finalize(records []*extract.Record) []*extract.Record {
	for _, r := range records {
		if r.Country == "" && r.CountryName != "" {
			r.Country = geoip.CountryToISO(r.CountryName)
		}
		r.CountryName = ""
		if r.ResponseTime != nil {
			ms := int(*r.ResponseTime) // python int(): truncates toward zero
			r.ResponseTimeMS = &ms
		} else {
			r.ResponseTimeMS = nil
		}
		r.ResponseTime = nil
		r.ResponseTimeRawMS = nil
	}
	return records
}

// sortedUnion is sorted(set(a) | set(b)).
func sortedUnion(a, b []string) []string {
	set := make(map[string]bool, len(a)+len(b))
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
