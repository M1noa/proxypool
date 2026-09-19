// apply a UAFilterSpec to the dataset: filter -> sort -> limit.

import type { UARecord } from "./ua-data";
import type { UAFilterSpec } from "./ua-params";

function major(v: string): string {
  return v.split(".")[0] ?? "";
}

function matches(r: UARecord, s: UAFilterSpec): boolean {
  if (s.browsers && !s.browsers.has(r.browser.toLowerCase())) return false;
  if (s.oses && !s.oses.has(r.os.toLowerCase())) return false;
  if (s.devices && !s.devices.has(r.device.toLowerCase())) return false;
  if (s.versions && !s.versions.has(major(r.browser_version))) return false;
  if (s.minShare !== null && (r.share ?? 0) < s.minShare) return false;
  if (s.maxShare !== null && (r.share ?? 0) > s.maxShare) return false;
  return true;
}

type Cmp = (a: UARecord, b: UARecord) => number;

function comparer(sort: UAFilterSpec["sort"]): Cmp | null {
  switch (sort) {
    case "share":
      return (a, b) => (a.share ?? 0) - (b.share ?? 0);
    case "browser":
      return (a, b) => (a.browser < b.browser ? -1 : a.browser > b.browser ? 1 : 0);
    case "version":
      return (a, b) =>
        a.browser_version < b.browser_version ? -1 : a.browser_version > b.browser_version ? 1 : 0;
    case "os":
      return (a, b) => (a.os < b.os ? -1 : a.os > b.os ? 1 : 0);
    case "device":
      return (a, b) => (a.device < b.device ? -1 : a.device > b.device ? 1 : 0);
    case "random":
      return null;
  }
}

export function applyUAFilters(records: UARecord[], spec: UAFilterSpec): UARecord[] {
  const out = records.filter((r) => matches(r, spec));

  if (spec.sort === "random") {
    for (let i = out.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      const a = out[i] as UARecord;
      out[i] = out[j] as UARecord;
      out[j] = a;
    }
  } else {
    const cmp = comparer(spec.sort);
    if (cmp) out.sort((a, b) => (spec.order === "asc" ? cmp(a, b) : cmp(b, a)));
  }

  return spec.limit > 0 ? out.slice(0, spec.limit) : out;
}
