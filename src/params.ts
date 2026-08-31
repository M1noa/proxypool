// query string -> validated, normalized filter spec.
// every rule about valid params lives here.

export type Format = "txt" | "json" | "jsonl" | "csv";
export type SortKey =
  | "response"
  | "reliability"
  | "quality"
  | "first_seen"
  | "last_seen"
  | "port"
  | "country"
  | "asn"
  | "random";
export type Order = "asc" | "desc";

export interface FilterSpec {
  types: Set<string> | null; // null = all
  anonymity: Set<string> | null; // "unknown" mapped to ""
  countries: Set<string> | null; // uppercase iso2
  ports: Set<number> | null;
  portMin: number | null;
  portMax: number | null;
  asns: Set<number> | null;
  asOrg: string | null; // lowercase substring
  sources: Set<string> | null; // lowercase
  ipVersion: "ipv4" | "ipv6" | null;
  ipTypes: Set<string> | null; // "unknown" mapped to ""
  https: boolean | null;
  minReliability: number | null;
  minQuality: number | null;
  responseMin: number | null;
  responseMax: number | null;
  firstSeenAfter: string | null; // iso 8601
  lastSeenAfter: string | null;
  sort: SortKey;
  order: Order;
  limit: number; // 0 = all
  format: Format;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

const PROTOCOLS = ["http", "https", "socks4", "socks5"] as const;
const ANONYMITY = ["elite", "anonymous", "transparent", "unknown"] as const;
const IP_TYPES = [
  "hosting",
  "business",
  "isp",
  "education_research",
  "government_admin",
  "unknown",
] as const;
const FORMATS = ["txt", "json", "jsonl", "csv"] as const;
const SORTS = [
  "response",
  "reliability",
  "quality",
  "first_seen",
  "last_seen",
  "port",
  "country",
  "asn",
  "random",
] as const;

// params we recognize; anything else is a 400
const KNOWN_PARAMS = new Set([
  "type",
  "protocol",
  "anonymity",
  "country",
  "port",
  "port_min",
  "port_max",
  "asn",
  "as_org",
  "source",
  "ip_version",
  "ip_type",
  "https",
  "min_reliability",
  "min_quality",
  "response_min",
  "response_max",
  "first_seen_after",
  "last_seen_after",
  "sort",
  "order",
  "limit",
  "format",
]);

function bad(message: string): never {
  throw new ApiError(400, "bad_request", message);
}

function getMulti(q: URLSearchParams, key: string): string[] {
  // accept ?x=a&x=b and ?x=a,b (and mixes)
  const out: string[] = [];
  for (const raw of q.getAll(key)) {
    for (const part of raw.split(",")) {
      const v = part.trim();
      if (v) out.push(v);
    }
  }
  return out;
}

function enumSet<T extends string>(
  values: string[],
  allowed: readonly T[],
  name: string,
  mapUnknown = false,
): Set<string> | null {
  if (values.length === 0) return null;
  const lower = values.map((v) => v.toLowerCase());
  if (lower.includes("any") || lower.includes("all")) return null;
  const out = new Set<string>();
  for (const v of lower) {
    if (!(allowed as readonly string[]).includes(v)) {
      bad(`invalid ${name} "${v}" — valid: ${allowed.join(", ")}, any`);
    }
    out.add(mapUnknown && v === "unknown" ? "" : v);
  }
  return out;
}

function intSet(values: string[], name: string): Set<number> | null {
  if (values.length === 0) return null;
  const out = new Set<number>();
  for (const v of values) {
    if (!/^\d{1,5}$/.test(v)) bad(`invalid ${name} "${v}" — must be an integer`);
    out.add(Number(v));
  }
  return out;
}

function intParam(q: URLSearchParams, key: string, min: number, max: number): number | null {
  const raw = q.get(key);
  if (raw === null || raw === "") return null;
  if (!/^\d+$/.test(raw)) bad(`invalid ${key} "${raw}" — must be an integer`);
  const n = Number(raw);
  if (n < min || n > max) bad(`invalid ${key} "${raw}" — must be ${min}..${max}`);
  return n;
}

function floatParam(q: URLSearchParams, key: string): number | null {
  const raw = q.get(key);
  if (raw === null || raw === "") return null;
  const n = Number(raw);
  if (!Number.isFinite(n) || n < 0 || n > 1) {
    bad(`invalid ${key} "${raw}" — must be a number between 0 and 1`);
  }
  return n;
}

function dateParam(q: URLSearchParams, key: string): string | null {
  const raw = q.get(key);
  if (raw === null || raw === "") return null;
  const t = Date.parse(raw);
  if (Number.isNaN(t)) bad(`invalid ${key} "${raw}" — must be an ISO 8601 date`);
  return new Date(t).toISOString();
}

export function parseParams(url: URL): FilterSpec {
  const q = url.searchParams;

  const unknown = [...new Set(q.keys())].filter((k) => !KNOWN_PARAMS.has(k));
  if (unknown.length > 0) {
    bad(`unknown param(s): ${unknown.join(", ")} — see /docs.json`);
  }

  const types = enumSet([...getMulti(q, "type"), ...getMulti(q, "protocol")], PROTOCOLS, "type");
  const anonymity = enumSet(getMulti(q, "anonymity"), ANONYMITY, "anonymity", true);
  const ipTypes = enumSet(getMulti(q, "ip_type"), IP_TYPES, "ip_type", true);

  const countryVals = getMulti(q, "country");
  let countries: Set<string> | null = null;
  if (countryVals.length > 0) {
    countries = new Set<string>();
    for (const c of countryVals) {
      if (!/^[a-zA-Z]{2}$/.test(c)) {
        bad(`invalid country "${c}" — must be a 2-letter ISO code`);
      }
      countries.add(c.toUpperCase());
    }
  }

  const sourceVals = getMulti(q, "source");
  const sources = sourceVals.length > 0 ? new Set(sourceVals.map((s) => s.toLowerCase())) : null;

  const ipVersionRaw = (q.get("ip_version") || "").toLowerCase();
  if (ipVersionRaw && ipVersionRaw !== "ipv4" && ipVersionRaw !== "ipv6") {
    bad(`invalid ip_version "${ipVersionRaw}" — valid: ipv4, ipv6`);
  }
  const ipVersion = ipVersionRaw === "" ? null : (ipVersionRaw as "ipv4" | "ipv6");

  const httpsRaw = (q.get("https") || "").toLowerCase();
  if (httpsRaw && httpsRaw !== "true" && httpsRaw !== "false") {
    bad(`invalid https "${httpsRaw}" — valid: true, false`);
  }
  const https = httpsRaw === "" ? null : httpsRaw === "true";

  const sortRaw = (q.get("sort") || "response").toLowerCase();
  if (!(SORTS as readonly string[]).includes(sortRaw)) {
    bad(`invalid sort "${sortRaw}" — valid: ${SORTS.join(", ")}`);
  }
  const sort = sortRaw as SortKey;

  const orderRaw = (q.get("order") || "").toLowerCase();
  if (orderRaw && orderRaw !== "asc" && orderRaw !== "desc") {
    bad(`invalid order "${orderRaw}" — valid: asc, desc`);
  }
  // response sorts fastest-first by default, everything else newest/highest-first
  const order: Order = orderRaw === "" ? (sort === "response" ? "asc" : "desc") : (orderRaw as Order);

  const formatRaw = (q.get("format") || "txt").toLowerCase();
  if (!(FORMATS as readonly string[]).includes(formatRaw)) {
    bad(`invalid format "${formatRaw}" — valid: ${FORMATS.join(", ")}`);
  }

  const asOrg = (q.get("as_org") || "").trim().toLowerCase() || null;

  const portMin = intParam(q, "port_min", 1, 65535);
  const portMax = intParam(q, "port_max", 1, 65535);
  if (portMin !== null && portMax !== null && portMin > portMax) {
    bad(`port_min (${portMin}) is greater than port_max (${portMax})`);
  }
  const responseMin = intParam(q, "response_min", 0, 600000);
  const responseMax = intParam(q, "response_max", 0, 600000);
  if (responseMin !== null && responseMax !== null && responseMin > responseMax) {
    bad(`response_min (${responseMin}) is greater than response_max (${responseMax})`);
  }

  return {
    types,
    anonymity,
    countries,
    ports: intSet(getMulti(q, "port"), "port"),
    portMin,
    portMax,
    asns: intSet(getMulti(q, "asn"), "asn"),
    asOrg,
    sources,
    ipVersion,
    ipTypes,
    https,
    minReliability: floatParam(q, "min_reliability"),
    minQuality: floatParam(q, "min_quality"),
    responseMin,
    responseMax,
    firstSeenAfter: dateParam(q, "first_seen_after"),
    lastSeenAfter: dateParam(q, "last_seen_after"),
    sort,
    order,
    limit: intParam(q, "limit", 0, 1000000) ?? 0,
    format: formatRaw as Format,
  };
}

// stable string for cache keys: same filters -> same key regardless of
// param order, repetition, or case
export function canonicalQuery(spec: FilterSpec): string {
  const parts: string[] = [];
  const pushSet = (name: string, s: Set<string> | null) => {
    if (s) parts.push(`${name}=${[...s].sort().join(",")}`);
  };
  const pushNumSet = (name: string, s: Set<number> | null) => {
    if (s) parts.push(`${name}=${[...s].sort((a, b) => a - b).join(",")}`);
  };
  pushSet("type", spec.types);
  pushSet("anon", spec.anonymity);
  pushSet("cc", spec.countries);
  pushNumSet("port", spec.ports);
  if (spec.portMin !== null) parts.push(`pmin=${spec.portMin}`);
  if (spec.portMax !== null) parts.push(`pmax=${spec.portMax}`);
  pushNumSet("asn", spec.asns);
  if (spec.asOrg) parts.push(`asorg=${spec.asOrg}`);
  pushSet("src", spec.sources);
  if (spec.ipVersion) parts.push(`ipv=${spec.ipVersion}`);
  pushSet("iptype", spec.ipTypes);
  if (spec.https !== null) parts.push(`https=${spec.https}`);
  if (spec.minReliability !== null) parts.push(`minrel=${spec.minReliability}`);
  if (spec.minQuality !== null) parts.push(`minq=${spec.minQuality}`);
  if (spec.responseMin !== null) parts.push(`rmin=${spec.responseMin}`);
  if (spec.responseMax !== null) parts.push(`rmax=${spec.responseMax}`);
  if (spec.firstSeenAfter) parts.push(`fsa=${spec.firstSeenAfter}`);
  if (spec.lastSeenAfter) parts.push(`lsa=${spec.lastSeenAfter}`);
  parts.push(`sort=${spec.sort}`);
  parts.push(`order=${spec.order}`);
  parts.push(`limit=${spec.limit}`);
  parts.push(`fmt=${spec.format}`);
  return parts.join("&");
}
