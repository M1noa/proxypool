// render filtered ua records as txt / json / jsonl / csv,
// and errors in whichever format was requested.

import type { UARecord } from "./ua-data";
import type { UAFormat } from "./ua-params";

const CSV_FIELDS = [
  "ua",
  "browser",
  "browser_version",
  "os",
  "os_version",
  "device",
  "share",
  "version_release_date",
  "generated_at",
  "template_source",
] as const;

function csvEscape(v: string): string {
  return /[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
}

function row(r: UARecord): string {
  return CSV_FIELDS.map((f) => {
    const v = r[f];
    return csvEscape(String(v ?? ""));
  }).join(",");
}

export function renderUA(records: UARecord[], format: UAFormat): string {
  switch (format) {
    case "txt":
      return records.map((r) => r.ua).join("\n") + (records.length ? "\n" : "");
    case "json":
      return JSON.stringify(records);
    case "jsonl":
      return records.map((r) => JSON.stringify(r)).join("\n") + (records.length ? "\n" : "");
    case "csv":
      return CSV_FIELDS.join(",") + "\n" + records.map(row).join("\n") + (records.length ? "\n" : "");
  }
}

export function renderUAError(format: UAFormat, status: number, code: string, message: string): string {
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
