// query string -> validated, normalized ua filter spec.
// mirrors params.ts: every rule about valid values lives here.

import { ApiError } from "./params";

export type UAFormat = "txt" | "json" | "jsonl" | "csv";
export type UASort = "share" | "browser" | "version" | "os" | "device" | "random";
export type UAOrder = "asc" | "desc";

export interface UAFilterSpec {
  browsers: Set<string> | null;
  oses: Set<string> | null;
  devices: Set<string> | null;
  versions: Set<string> | null; // browser major, e.g. "154"
  minShare: number | null;
  maxShare: number | null;
  sort: UASort;
  order: UAOrder;
  limit: number; // 0 = all
  format: UAFormat;
}

const BROWSERS = [
  "chrome",
  "firefox",
  "safari",
  "edge",
  "opera",
  "samsung",
  "vivaldi",
  "yandex",
  "ie",
] as const;
const OSES = ["windows", "macos", "linux", "android", "ios", "chromeos"] as const;
const DEVICES = ["desktop", "mobile", "tablet"] as const;
const FORMATS = ["txt", "json", "jsonl", "csv"] as const;
const SORTS = ["share", "browser", "version", "os", "device", "random"] as const;

const KNOWN_PARAMS = new Set([
  "browser",
  "os",
  "device",
  "version",
  "min_share",
  "max_share",
  "sort",
  "order",
  "limit",
  "format",
]);

function bad(message: string): never {
  throw new ApiError(400, "bad_request", message);
}

function getMulti(q: URLSearchParams, key: string): string[] {
  const out: string[] = [];
  for (const raw of q.getAll(key)) {
    for (const part of raw.split(",")) {
      const v = part.trim();
      if (v) out.push(v);
    }
  }
  return out;
}

function enumSet<T extends string>(values: string[], allowed: readonly T[], name: string): Set<string> | null {
  if (values.length === 0) return null;
  const lower = values.map((v) => v.toLowerCase());
  if (lower.includes("any") || lower.includes("all")) return null;
  const out = new Set<string>();
  for (const v of lower) {
    if (!(allowed as readonly string[]).includes(v)) {
      bad(`invalid ${name} "${v}" — valid: ${allowed.join(", ")}, any`);
    }
    out.add(v);
  }
  return out;
}

function shareParam(q: URLSearchParams, key: string): number | null {
  const raw = q.get(key);
  if (raw === null || raw === "") return null;
  const n = Number(raw);
  if (!Number.isFinite(n) || n < 0 || n > 1) {
    bad(`invalid ${key} "${raw}" — must be a number between 0 and 1`);
  }
  return n;
}

function intParam(q: URLSearchParams, key: string, min: number, max: number): number | null {
  const raw = q.get(key);
  if (raw === null || raw === "") return null;
  if (!/^\d+$/.test(raw)) bad(`invalid ${key} "${raw}" — must be an integer`);
  const n = Number(raw);
  if (n < min || n > max) bad(`invalid ${key} "${raw}" — must be ${min}..${max}`);
  return n;
}

export function parseUAParams(url: URL): UAFilterSpec {
  const q = url.searchParams;

  const unknown = [...new Set(q.keys())].filter((k) => !KNOWN_PARAMS.has(k));
  if (unknown.length > 0) {
    bad(`unknown param(s): ${unknown.join(", ")} — see /docs.json`);
  }

  const versionVals = getMulti(q, "version");
  let versions: Set<string> | null = null;
  if (versionVals.length > 0) {
    versions = new Set<string>();
    for (const v of versionVals) {
      if (!/^\d{1,4}$/.test(v)) bad(`invalid version "${v}" — must be a browser major number`);
      versions.add(v);
    }
  }

  const sortRaw = (q.get("sort") || "share").toLowerCase();
  if (!(SORTS as readonly string[]).includes(sortRaw)) {
    bad(`invalid sort "${sortRaw}" — valid: ${SORTS.join(", ")}`);
  }
  const sort = sortRaw as UASort;

  const orderRaw = (q.get("order") || "").toLowerCase();
  if (orderRaw && orderRaw !== "asc" && orderRaw !== "desc") {
    bad(`invalid order "${orderRaw}" — valid: asc, desc`);
  }
  // share sorts highest-first by default, everything else a-z
  const order: UAOrder =
    orderRaw === "" ? (sort === "share" ? "desc" : "asc") : (orderRaw as UAOrder);

  const formatRaw = (q.get("format") || "txt").toLowerCase();
  if (!(FORMATS as readonly string[]).includes(formatRaw)) {
    bad(`invalid format "${formatRaw}" — valid: ${FORMATS.join(", ")}`);
  }

  const minShare = shareParam(q, "min_share");
  const maxShare = shareParam(q, "max_share");
  if (minShare !== null && maxShare !== null && minShare > maxShare) {
    bad(`min_share (${minShare}) is greater than max_share (${maxShare})`);
  }

  return {
    browsers: enumSet(getMulti(q, "browser"), BROWSERS, "browser"),
    oses: enumSet(getMulti(q, "os"), OSES, "os"),
    devices: enumSet(getMulti(q, "device"), DEVICES, "device"),
    versions,
    minShare,
    maxShare,
    sort,
    order,
    limit: intParam(q, "limit", 0, 1000000) ?? 0,
    format: formatRaw as UAFormat,
  };
}

// --- shortcut routes: /ua-<name>.<ext> --------------------------------------
// one filter dimension pinned by the path, everything else from the query
// string. /ua-all.<ext> pins nothing.

export type UAShortcutDim = "browser" | "os" | "device";

export interface UAShortcut {
  name: string;
  dim: UAShortcutDim | null;
  value: string | null;
  format: UAFormat;
}

const SHORTCUTS: Record<string, { dim: UAShortcutDim | null; value: string | null }> = {
  "ua-all": { dim: null, value: null },

  "ua-chrome": { dim: "browser", value: "chrome" },
  "ua-firefox": { dim: "browser", value: "firefox" },
  "ua-safari": { dim: "browser", value: "safari" },
  "ua-edge": { dim: "browser", value: "edge" },
  "ua-opera": { dim: "browser", value: "opera" },
  "ua-samsung": { dim: "browser", value: "samsung" },

  "ua-windows": { dim: "os", value: "windows" },
  "ua-macos": { dim: "os", value: "macos" },
  "ua-linux": { dim: "os", value: "linux" },
  "ua-android": { dim: "os", value: "android" },
  "ua-ios": { dim: "os", value: "ios" },

  "ua-desktop": { dim: "device", value: "desktop" },
  "ua-mobile": { dim: "device", value: "mobile" },
  "ua-tablet": { dim: "device", value: "tablet" },
};

const SHORTCUT_PATH = /^\/(ua-[a-z0-9_]+)\.(txt|json|jsonl|csv)$/;

export function matchUAShortcut(pathname: string): UAShortcut | null {
  const m = SHORTCUT_PATH.exec(pathname);
  if (!m) return null;
  const [, name, ext] = m;
  if (!name || !ext) return null;
  const entry = SHORTCUTS[name];
  if (!entry) return null;
  return { name, dim: entry.dim, value: entry.value, format: ext as UAFormat };
}

export function specFromUAShortcut(url: URL, shortcut: UAShortcut): UAFilterSpec {
  const q = new URLSearchParams(url.search);
  q.set("format", shortcut.format);
  if (shortcut.dim && shortcut.value) {
    q.set(shortcut.dim, shortcut.value);
  }
  return parseUAParams(new URL(`${url.origin}/uas?${q.toString()}`));
}

export function canonicalUAQuery(spec: UAFilterSpec): string {
  const parts: string[] = [];
  const pushSet = (name: string, s: Set<string> | null) => {
    if (s) parts.push(`${name}=${[...s].sort().join(",")}`);
  };
  pushSet("browser", spec.browsers);
  pushSet("os", spec.oses);
  pushSet("device", spec.devices);
  pushSet("version", spec.versions);
  if (spec.minShare !== null) parts.push(`minshare=${spec.minShare}`);
  if (spec.maxShare !== null) parts.push(`maxshare=${spec.maxShare}`);
  parts.push(`sort=${spec.sort}`);
  parts.push(`order=${spec.order}`);
  parts.push(`limit=${spec.limit}`);
  parts.push(`fmt=${spec.format}`);
  return parts.join("&");
}
