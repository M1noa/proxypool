"""fetch every source endpoint (no proxy checking) and report per-source health.

usage:
  python3 probe_endpoints.py [--workers N] [--json out.json]

reports each source: status ok/empty/error, record count, errors.
helps spot dead endpoints, broken extract configs, and pagination issues.
"""
import argparse
import json
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib.parse import fetch_source
from lib.util import load_jsonc

ROOT = Path(__file__).resolve().parent


def probe(src):
    t0 = time.time()
    try:
        recs, errors = fetch_source(src)
    except Exception as e:
        return {"name": src["name"], "format": src.get("format", "text"),
                "protocol": src.get("protocol") or "mixed",
                "status": "crash", "count": 0, "seconds": 0,
                "errors": [f"crashed: {e}"]}
    dt = time.time() - t0
    if errors:
        status = "error"
    elif not recs:
        status = "empty"
    else:
        status = "ok"
    return {"name": src["name"], "format": src.get("format", "text"),
            "protocol": src.get("protocol") or "mixed", "status": status,
            "count": len(recs), "seconds": round(dt, 1), "errors": errors}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=10)
    ap.add_argument("--json", default="", help="write full results to this path")
    args = ap.parse_args()

    cfg = load_jsonc(ROOT / "sources.jsonc")
    sources = cfg["sources"]
    print(f"probing {len(sources)} sources (workers={args.workers})...\n")

    results = []
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futs = {pool.submit(probe, s): s for s in sources}
        for fut in as_completed(futs):
            r = fut.result()
            results.append(r)
            mark = {"ok": ".", "empty": "EMPTY", "error": "ERR", "crash": "CRASH"}[r["status"]]
            print(f"[{mark:5}] {r['name']:22} {r['count']:>7}  ({r['seconds']}s)")

    results.sort(key=lambda r: (r["status"] != "ok", r["status"], r["name"]))
    ok = [r for r in results if r["status"] == "ok"]
    empty = [r for r in results if r["status"] == "empty"]
    bad = [r for r in results if r["status"] in ("error", "crash")]

    print("\n===== summary =====")
    print(f"ok={len(ok)} empty={len(empty)} error/crash={len(bad)} "
          f"total={len(results)} total_proxies={sum(r['count'] for r in results)}")

    if empty:
        print("\nEMPTY (returned 0 records):")
        for r in empty:
            print(f"  {r['name']:22} {r['format']:6} {r['protocol']}")
    if bad:
        print("\nFAILED:")
        for r in bad:
            for e in r["errors"]:
                print(f"  {r['name']:22} {e}")

    if args.json:
        Path(args.json).write_text(json.dumps(results, indent=2))
        print(f"\nwrote {args.json}")


if __name__ == "__main__":
    main()