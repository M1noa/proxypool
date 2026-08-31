// apply a FilterSpec to the dataset: filter -> sort -> limit.

import type { ProxyRecord } from "./data";
import type { FilterSpec } from "./params";

function matches(r: ProxyRecord, s: FilterSpec): boolean {
  if (s.types && !s.types.has("")) {
    // proxy must support at least one selected protocol
    if (!r.protocols.some((p) => s.types!.has(p))) return false;
  }
  if (s.anonymity && !s.anonymity.has(r.anonymity || "")) return false;
  if (s.countries && !s.countries.has((r.country || "").toUpperCase())) return false;
  if (s.ports && !s.ports.has(r.port)) return false;
  if (s.portMin !== null && r.port < s.portMin) return false;
  if (s.portMax !== null && r.port > s.portMax) return false;
  if (s.asns && !s.asns.has(r.asn)) return false;
  if (s.asOrg && !(r.as_org || "").toLowerCase().includes(s.asOrg)) return false;
  if (s.sources && !r.sources.some((src) => s.sources!.has(src.toLowerCase()))) {
    return false;
  }
  if (s.ipVersion && r.ip_version !== s.ipVersion) return false;
  if (s.ipTypes && !s.ipTypes.has(r.ip_type || "")) return false;
  if (s.https !== null && r.https !== s.https) return false;
  if (s.minReliability !== null && (r.reliability ?? 0) < s.minReliability) return false;
  if (s.minQuality !== null && (r.quality ?? 0) < s.minQuality) return false;
  if (s.responseMin !== null && (r.response_time_ms ?? Infinity) < s.responseMin) return false;
  if (s.responseMax !== null && (r.response_time_ms ?? Infinity) > s.responseMax) return false;
  // iso 8601 utc strings compare lexicographically
  if (s.firstSeenAfter && (r.first_seen || "") < s.firstSeenAfter) return false;
  if (s.lastSeenAfter && (r.last_seen || "") < s.lastSeenAfter) return false;
  return true;
}

type Cmp = (a: ProxyRecord, b: ProxyRecord) => number;

function comparer(sort: FilterSpec["sort"]): Cmp | null {
  switch (sort) {
    case "response":
      return (a, b) => (a.response_time_ms ?? Infinity) - (b.response_time_ms ?? Infinity);
    case "reliability":
      return (a, b) => (a.reliability ?? 0) - (b.reliability ?? 0);
    case "quality":
      return (a, b) => (a.quality ?? 0) - (b.quality ?? 0);
    case "first_seen":
      return (a, b) => (a.first_seen < b.first_seen ? -1 : a.first_seen > b.first_seen ? 1 : 0);
    case "last_seen":
      return (a, b) => (a.last_seen < b.last_seen ? -1 : a.last_seen > b.last_seen ? 1 : 0);
    case "port":
      return (a, b) => a.port - b.port;
    case "country":
      return (a, b) => (a.country < b.country ? -1 : a.country > b.country ? 1 : 0);
    case "asn":
      return (a, b) => (a.asn ?? 0) - (b.asn ?? 0);
    case "random":
      return null; // handled by shuffle
  }
}

export function applyFilters(records: ProxyRecord[], spec: FilterSpec): ProxyRecord[] {
  const out = records.filter((r) => matches(r, spec));

  if (spec.sort === "random") {
    // fisher-yates; Math.random is fine here (not security-sensitive)
    for (let i = out.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      const a = out[i] as ProxyRecord;
      out[i] = out[j] as ProxyRecord;
      out[j] = a;
    }
  } else {
    const cmp = comparer(spec.sort);
    if (cmp) out.sort((a, b) => (spec.order === "asc" ? cmp(a, b) : cmp(b, a)));
  }

  return spec.limit > 0 ? out.slice(0, spec.limit) : out;
}
