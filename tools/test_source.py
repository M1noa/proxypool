"""fetch a single source (or all) and print sample proxies without checking.

usage:
  python3 test_source.py <name-substring> [--max-pages N] [--show N] [--list]
  python3 test_source.py --list
  python3 test_source.py charlespikachu --show 5

useful for verifying a new source entry in sources.jsonc before a full run.
"""
import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from lib.parse import fetch_source
from lib.util import load_jsonc

ROOT = Path(__file__).resolve().parent.parent


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("name", nargs="?", help="substring match against source name/display")
    ap.add_argument("--max-pages", type=int, default=0, help="cap pages (0 = source default)")
    ap.add_argument("--show", type=int, default=10, help="sample records to print")
    ap.add_argument("--list", action="store_true", help="list sources and exit")
    args = ap.parse_args()

    cfg = load_jsonc(ROOT / "sources.jsonc")
    sources = cfg["sources"]

    if args.list:
        for s in sources:
            print(f"{s['name']:24} {s.get('format',''):6} "
                  f"{'antibot ' if s.get('antibot') else '       '}"
                  f"{'flow:'+s['flow'] if s.get('flow') else ''}")
        return

    if not args.name:
        ap.error("provide a source name/substring or --list")

    match = [s for s in sources
             if args.name.lower() in s["name"].lower()
             or args.name.lower() in (s.get("display") or "").lower()]
    if not match:
        print(f"no source matches {args.name!r}")
        return

    for src in match:
        if args.max_pages:
            src = dict(src)
            if src.get("pagination"):
                src["pagination"] = dict(src["pagination"], max_pages=args.max_pages)
            else:
                src["pagination"] = {"type": "page", "start": 1, "step": 1,
                                      "stop": "empty", "max_pages": args.max_pages}
        print(f"\n=== {src['name']} ({src.get('display','')}) ===")
        recs, errors = fetch_source(src)
        print(f"fetched {len(recs)} raw records")
        for r in recs[:args.show]:
            try:
                d = dict(r._asdict()) if hasattr(r, "_asdict") else dict(r)
            except Exception:
                d = vars(r)
            for k, v in list(d.items()):
                if isinstance(v, set):
                    d[k] = sorted(v)
            print(json.dumps(d, ensure_ascii=False))
        for e in errors:
            print("ERR:", e)


if __name__ == "__main__":
    main()
