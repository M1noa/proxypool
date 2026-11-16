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
let retryAt = 0;

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
      cf: { cacheTtl: Number(env.CACHE_TTL_SECONDS || "30"), cacheEverything: true },
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

  // bounded payload (a few mb), safe to buffer. a body read can fail on its own
  // (reset mid-transfer, truncated edge object) with headers already ok, and
  // that used to surface as a bare 500 -- keep it in the api error family
  let text: string;
  try {
    text = await res.text();
  } catch (e) {
    throw new ApiError(
      502,
      "upstream_truncated",
      `upstream body read failed: ${e instanceof Error ? e.message : String(e)}`,
    );
  }

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

// after a failed refresh, hold off before trying upstream again -- otherwise
// every request in a stale isolate retries the same broken fetch
const RETRY_COOLDOWN_MS = 30_000;

export async function getDataset(env: Env): Promise<Dataset> {
  const ttlMs = Number(env.CACHE_TTL_SECONDS || "30") * 1000;
  const now = Date.now();
  if (cached && (now - cached.fetchedAt < ttlMs || now < retryAt)) return cached;

  // coalesce concurrent refreshes within one isolate
  inflight ??= fetchUpstream(env).finally(() => {
    inflight = null;
  });
  try {
    cached = await inflight;
    retryAt = 0;
    return cached;
  } catch (e) {
    // a failed refresh is no reason to stop serving the dataset we already
    // have: it is at worst a few minutes older than the one we wanted
    const stale = cached;
    if (!stale) throw e;
    retryAt = Date.now() + RETRY_COOLDOWN_MS;
    console.error(
      JSON.stringify({
        message: "upstream refresh failed, serving stale",
        age_s: Math.round((Date.now() - stale.fetchedAt) / 1000),
        error: e instanceof Error ? `${e.name}: ${e.message}` : String(e),
      }),
    );
    return stale;
  }
}
