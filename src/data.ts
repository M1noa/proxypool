// upstream proxies.json fetch + cache.
// module-level cache holds the parsed dataset only (immutable, never request
// state) so warm isolates skip both the network and the json parse.

import { ApiError } from "./params";

export interface ProxyRecord {
  ip: string;
  ip_version: string;
  port: number;
  protocols: string[];
  country: string;
  anonymity: string;
  https: boolean;
  sources: string[];
  source_meta: Record<string, unknown>;
  last_checked: string;
  response_time_ms: number;
  response_time_raw_ms: number;
  asn: number;
  as_org: string;
  ip_type: string;
  reliability: number;
  quality: number;
  checks_total: number;
  checks_ok: number;
  first_seen: string;
  last_seen: string;
}

export interface Dataset {
  records: ProxyRecord[];
  raw: string; // original upstream body, for bare-/list passthrough
  fetchedAt: number; // ms epoch
}

let cached: Dataset | null = null;
let inflight: Promise<Dataset> | null = null;

function isRecord(v: unknown): v is ProxyRecord {
  if (typeof v !== "object" || v === null) return false;
  const r = v as Record<string, unknown>;
  return (
    typeof r.ip === "string" &&
    typeof r.port === "number" &&
    Array.isArray(r.protocols)
  );
}

async function fetchUpstream(env: Env): Promise<Dataset> {
  let res: Response;
  try {
    res = await fetch(env.UPSTREAM_URL, {
      cf: { cacheTtl: 30, cacheEverything: true },
      headers: { "User-Agent": "proxies.minoa.cat worker" },
    });
  } catch (e) {
    throw new ApiError(
      502,
      "upstream_unreachable",
      `could not reach upstream data source: ${e instanceof Error ? e.message : String(e)}`,
    );
  }
  if (!res.ok) {
    throw new ApiError(502, "upstream_error", `upstream returned HTTP ${res.status}`);
  }

  // bounded payload (a few mb), safe to buffer
  const text = await res.text();

  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new ApiError(502, "upstream_bad_json", "upstream did not return valid json");
  }
  if (!Array.isArray(raw)) {
    throw new ApiError(502, "upstream_bad_shape", "upstream json is not an array");
  }

  // drop malformed entries instead of failing the whole dataset
  const records = raw.filter(isRecord);
  return { records, raw: text, fetchedAt: Date.now() };
}

export async function getDataset(env: Env): Promise<Dataset> {
  const ttlMs = Number(env.CACHE_TTL_SECONDS || "30") * 1000;
  if (cached && Date.now() - cached.fetchedAt < ttlMs) return cached;

  // coalesce concurrent refreshes within one isolate
  inflight ??= fetchUpstream(env).finally(() => {
    inflight = null;
  });
  cached = await inflight;
  return cached;
}
