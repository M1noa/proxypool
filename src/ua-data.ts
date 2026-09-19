// upstream useragents.json fetch + cache. mirrors data.ts: module-level
// cache holds the parsed dataset only, never request state.

import { ApiError } from "./params";

export interface UARecord {
  ua: string;
  browser: string;
  browser_version: string;
  os: string;
  os_version: string;
  device: string;
  share: number;
  version_release_date: string;
  generated_at: string;
  template_source: string;
}

export interface UADataset {
  records: UARecord[];
  raw: string; // original upstream body, for bare-/uas passthrough
  fetchedAt: number; // ms epoch
}

let cached: UADataset | null = null;
let inflight: Promise<UADataset> | null = null;

function isRecord(v: unknown): v is UARecord {
  if (typeof v !== "object" || v === null) return false;
  const r = v as Record<string, unknown>;
  return typeof r.ua === "string" && typeof r.browser === "string";
}

async function fetchUpstream(env: Env): Promise<UADataset> {
  let res: Response;
  try {
    res = await fetch(env.UAS_URL, {
      cf: { cacheTtl: Number(env.CACHE_TTL_SECONDS || "30"), cacheEverything: true },
      headers: { "User-Agent": "proxies.minoa.cat worker" },
    });
  } catch (e) {
    throw new ApiError(
      502,
      "upstream_unreachable",
      `could not reach ua upstream: ${e instanceof Error ? e.message : String(e)}`,
    );
  }
  if (!res.ok) {
    throw new ApiError(502, "upstream_error", `ua upstream returned HTTP ${res.status}`);
  }

  const text = await res.text();

  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new ApiError(502, "upstream_bad_json", "ua upstream did not return valid json");
  }
  if (!Array.isArray(raw)) {
    throw new ApiError(502, "upstream_bad_shape", "ua upstream json is not an array");
  }

  const records = raw.filter(isRecord);
  return { records, raw: text, fetchedAt: Date.now() };
}

export async function getUADataset(env: Env): Promise<UADataset> {
  const ttlMs = Number(env.CACHE_TTL_SECONDS || "30") * 1000;
  if (cached && Date.now() - cached.fetchedAt < ttlMs) return cached;

  inflight ??= fetchUpstream(env).finally(() => {
    inflight = null;
  });
  cached = await inflight;
  return cached;
}
