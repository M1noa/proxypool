"""fetch all sources, merge into unified records, write output files.

usage: python3 fetch_proxies.py [--no-fetch-cache]
outputs:
  output/proxies.json            all records, sorted by response time
  output/{http,https,socks4,socks5}.txt   ip:port lines
"""
import json
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib.parse import fetch_source
from lib.util import load_jsonc

ROOT = Path(__file__).resolve().parent
OUT = ROOT / "output"

ANON_RANK = {"": 0, "transparent": 1, "anonymous": 2, "elite": 3}


def country_to_iso(name):
    if not name:
        return ""
    import pycountry

    try:
        c = pycountry.countries.lookup(name)
        return c.alpha_2
    except LookupError:
        # names like "France - Lauterbourg" — try the first segment
        head = name.split(" - ")[0].split(",")[0].strip()
        try:
            return pycountry.countries.lookup(head).alpha_2
        except LookupError:
            return ""


def merge(all_records):
    """key=ip:port; union lists; strongest value wins; keep all source_meta"""
    merged = {}
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    for rec in all_records:
        key = f"{rec['ip']}:{rec['port']}"
        if key not in merged:
            merged[key] = rec
            continue
        cur = merged[key]

        def extend(field):
            cur[field] = sorted(set(cur.get(field, [])) | set(rec.get(field, [])))

        extend("sources")

        protos = set(cur["protocols"]) | set(rec["protocols"])
        cur["protocols"] = sorted(protos)

        if not cur["country"] and rec["country"]:
            cur["country"] = rec["country"]
        if not cur["country_name"] and rec["country_name"]:
            cur["country_name"] = rec["country_name"]

        if ANON_RANK[rec["anonymity"]] > ANON_RANK[cur["anonymity"]]:
            cur["anonymity"] = rec["anonymity"]

        cur["https"] = bool(cur["https"] or rec["https"])

        # own-check values overwrite; otherwise keep fastest reported
        if rec["response_time"] is not None:
            if cur["response_time"] is None or rec["response_time"] < cur["response_time"]:
                cur["response_time"] = rec["response_time"]

        for k, v in rec["source_meta"].items():
            if k not in cur["source_meta"]:
                cur["source_meta"][k] = v
    out = list(merged.values())
    for r in out:
        r["last_checked"] = None  # filled by checker phase
    del now
    return out


def finalize(records):
    """country_name -> ISO where missing; normalize response time field"""
    for r in records:
        if not r["country"] and r["country_name"]:
            r["country"] = country_to_iso(r["country_name"])
        r.pop("country_name", None)
        # source-reported ms; checker phase overwrites with calibrated values
        rt = r.pop("response_time", None)
        r["response_time_ms"] = int(rt) if rt is not None else None
    return records


def sort_records(records):
    return sorted(records, key=lambda r: (
        r.get("response_time_ms") is None,
        r.get("response_time_ms") or float("inf"),
    ))


def write_outputs(records):
    OUT.mkdir(exist_ok=True)
    records = sort_records(records)
    (OUT / "proxies.json").write_text(json.dumps(records, indent=2))

    buckets = {"http.txt": [], "https.txt": [], "socks4.txt": [], "socks5.txt": []}
    for r in records:
        line = f"{r['ip']}:{r['port']}\n"
        ps = set(r["protocols"])
        if "http" in ps:
            buckets["http.txt"].append(line)
        if "https" in ps or r["https"]:
            buckets["https.txt"].append(line)
        if "socks4" in ps:
            buckets["socks4.txt"].append(line)
        if "socks5" in ps:
            buckets["socks5.txt"].append(line)
    for name, lines in buckets.items():
        (OUT / name).write_text("".join(lines))
    return {k: len(v) for k, v in buckets.items()}


def main():
    cfg = load_jsonc(ROOT / "sources.jsonc")
    sources = cfg["sources"]
    all_records = []
    errors = []
    with ThreadPoolExecutor(max_workers=12) as pool:
        futs = {pool.submit(fetch_source, s): s["name"] for s in sources}
        for fut in as_completed(futs):
            name = futs[fut]
            try:
                recs, errs = fut.result()
            except Exception as e:
                errors.append(f"{name}: crashed: {e}")
                continue
            errors.extend(errs)
            all_records.extend(recs)
            print(f"{name:24} {len(recs):6}", flush=True)

    print(f"\nfetched {len(all_records)} raw records from {len(sources)} sources")
    records = finalize(merge(all_records))
    counts = write_outputs(records)
    print(f"unique proxies: {len(records)}")
    print(f"http={counts['http.txt']} https={counts['https.txt']} "
          f"socks4={counts['socks4.txt']} socks5={counts['socks5.txt']}")
    if errors:
        print("\nerrors:")
        for e in errors:
            print(" ", e)


if __name__ == "__main__":
    main()
