// package pipeline ports fetch_proxies.py main(): fetch every source, merge by
// ip:port, fill in geoip/asn, probe what's alive, score it against history,
// and write the outputs.
package pipeline

import (
	"slices"

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
		// both maps are nil until a record actually has something to put in one,
		// so the merge target may need allocating first
		if len(rec.Provided) > 0 {
			if cur.Provided == nil {
				cur.Provided = make(map[string]bool, len(rec.Provided))
			}
			for f := range rec.Provided {
				cur.Provided[f] = true
			}
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
		if len(rec.SourceMeta) > 0 {
			if cur.SourceMeta == nil {
				cur.SourceMeta = make(map[string]any, len(rec.SourceMeta))
			}
			for k, v := range rec.SourceMeta {
				if _, ok := cur.SourceMeta[k]; !ok {
					cur.SourceMeta[k] = v
				}
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

// sortedUnion is sorted(set(a) | set(b)). concatenate-sort-compact rather than a
// set: protocols top out at 4 and sources at however many republished one proxy,
// and at those sizes hashing every element costs more than the sort. measured
// 3x faster on protocols and 5x on a 9-source union, at one allocation instead
// of four.
func sortedUnion(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	slices.Sort(out)
	return slices.Compact(out)
}
