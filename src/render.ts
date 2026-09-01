// render filtered records as txt / json / jsonl / csv,
// and render errors in whichever format was requested.

import type { ProxyRecord } from "./data";
import type { FilterSpec, Format } from "./params";

export const CONTENT_TYPES: Record<Format, string> = {
  txt: "text/plain; charset=utf-8",
  json: "application/json; charset=utf-8",
  jsonl: "application/x-ndjson; charset=utf-8",
  csv: "text/csv; charset=utf-8",
};

const CSV_FIELDS = [
  "ip",
  "port",
  "protocols",
  "ip_version",
  "country",
  "anonymity",
  "https",
  "asn",
  "as_org",
  "ip_type",
  "response_time_ms",
  "response_time_raw_ms",
  "reliability",
  "quality",
  "checks_total",
  "checks_ok",
  "sources",
  "first_seen",
  "last_seen",
  "last_checked",
] as const;

function csvEscape(v: string): string {
  return /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}

function row(r: ProxyRecord): string {
  return CSV_FIELDS.map((f) => {
    const v = r[f];
    if (Array.isArray(v)) return csvEscape(v.join(";"));
    if (typeof v === "boolean") return v ? "true" : "false";
    return csvEscape(String(v ?? ""));
  }).join(",");
}

// txt: one type selected -> bare ip:port. zero or 2+ -> protocol://ip:port,
// one line per matched protocol per proxy.
function renderTxt(records: ProxyRecord[], spec: FilterSpec): string {
  const single = spec.types && spec.types.size === 1;
  if (single) {
    return records.map((r) => `${r.ip}:${r.port}`).join("\n") + "\n";
  }
  const lines: string[] = [];
  for (const r of records) {
    for (const p of r.protocols) {
      if (!spec.types || spec.types.has(p)) lines.push(`${p}://${r.ip}:${r.port}`);
    }
  }
  return lines.join("\n") + (lines.length ? "\n" : "");
}

export function render(records: ProxyRecord[], spec: FilterSpec): string {
  switch (spec.format) {
    case "txt":
      return renderTxt(records, spec);
    case "json":
      return JSON.stringify(records);
    case "jsonl":
      return records.map((r) => JSON.stringify(r)).join("\n") + (records.length ? "\n" : "");
    case "csv":
      return CSV_FIELDS.join(",") + "\n" + records.map(row).join("\n") + (records.length ? "\n" : "");
  }
}

// errors rendered in the requested format, so scripts can parse failures
export function renderError(format: Format, status: number, code: string, message: string): string {
  switch (format) {
    case "txt":
      return `error ${status} (${code}): ${message}\n`;
    case "json":
      return JSON.stringify({ error: { status, code, message } }, null, 2) + "\n";
    case "jsonl":
      return JSON.stringify({ error: { status, code, message } }) + "\n";
    case "csv":
      return `error,message\n${csvEscape(`${status} ${code}`)},${csvEscape(message)}\n`;
  }
}
