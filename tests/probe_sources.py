"""phase 1 probe: fetch first response from every source, dump head/tail.

usage: python3 tests/probe_sources.py [source_name ...]
writes raw responses to test_output/raw/<name>.txt (truncated) and prints
head+tail snippets so schemas can be documented.
"""
import json
import re
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import requests

ROOT = Path(__file__).resolve().parent.parent
SOURCES_FILE = ROOT / "sources.jsonc"
RAW_DIR = ROOT / "test_output" / "raw"
MAX_DUMP = 100_000
TIMEOUT = 25


def load_jsonc(path):
    text = re.sub(r"^\s*//.*$", "", path.read_text(), flags=re.M)
    return json.loads(text)


def fetch(source):
    name = source["name"]
    url = source["url"]
    # first page/offset only
    url = url.replace("{page}", str(source.get("pagination", {}).get("start", 1)))
    url = url.replace("{offset}", str(source.get("pagination", {}).get("start", 0)))
    headers = dict(source.get("headers", {}))
    headers.setdefault("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
    try:
        if source.get("method") == "POST":
            resp = requests.post(url, json=source.get("body"), headers=headers, timeout=TIMEOUT)
        else:
            resp = requests.get(url, headers=headers, timeout=TIMEOUT)
        body = resp.text
        status = resp.status_code
    except Exception as exc:
        return name, f"FETCH ERROR: {exc!r}"

    out = RAW_DIR / f"{name}.txt"
    out.write_text(body[:MAX_DUMP])
    snippet = body.strip()
    head = snippet[:600]
    tail = snippet[-300:] if len(snippet) > 900 else ""
    return name, f"[{status}] {len(body)} bytes -> {out.name}\nHEAD: {head}\nTAIL: {tail}"


def main():
    cfg = load_jsonc(SOURCES_FILE)
    wanted = set(sys.argv[1:])
    sources = [s for s in cfg["sources"] if not wanted or s["name"] in wanted]
    RAW_DIR.mkdir(parents=True, exist_ok=True)
    with ThreadPoolExecutor(max_workers=10) as pool:
        futures = {pool.submit(fetch, s): s["name"] for s in sources}
        for fut in as_completed(futures):
            name, report = fut.result()
            print(f"\n{'='*70}\n## {name}\n{report}")


if __name__ == "__main__":
    main()
