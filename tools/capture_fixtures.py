#!/usr/bin/env python3
"""capture raw source bodies + lib/parse output as go test fixtures.

the go port is verified differentially against the python it replaces, so the
gate needs both halves of the same bytes: what the source served, and what
parse_content made of it.

    python3 tools/capture_fixtures.py /tmp/pfx
    PROXYPOOL_FIXTURES=/tmp/pfx go test ./internal/extract/

only the first page of each source is fetched, and flows are skipped — they do
their own fetching, so there is no single body to capture.
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from lib.parse import _run_prefetch, parse_content
from lib.util import load_jsonc, make_session, request


def capture(src, out_dir):
    sess = make_session(src)
    deadline = time.monotonic() + 25

    url = src.get("url")
    headers = {}
    if src.get("prefetch"):
        url, headers = _run_prefetch(src, sess, deadline)
    if not url and src.get("urls"):
        url = src["urls"][0]["url"]
    url = url.replace("{page}", "1").replace("{offset}", "0")

    body = src.get("body")
    if isinstance(body, dict):
        body = {k: (1 if isinstance(v, str) and v in ("{page}", "{offset}") else v)
                for k, v in body.items()}

    content = request(url, method=src.get("method", "GET"), body=body,
                      body_type=src.get("body_type"), timeout=15,
                      session=sess, headers=headers or None, deadline=deadline)

    defaults = src["urls"][0].get("set") if src.get("urls") else None
    recs = parse_content(src, content, defaults)
    for r in recs:
        r["_provided"] = sorted(r["_provided"])  # sets are not json

    with open(os.path.join(out_dir, src["name"] + ".raw"), "w") as f:
        f.write(content)
    return {"defaults": defaults, "records": recs}


def main():
    out_dir = sys.argv[1] if len(sys.argv) > 1 else "/tmp/pfx"
    only = set(sys.argv[2:])
    os.makedirs(out_dir, exist_ok=True)

    srcs = load_jsonc("sources.jsonc")["sources"]
    out = {}
    for src in srcs:
        name = src["name"]
        if src.get("flow") or (only and name not in only):
            continue
        try:
            out[name] = capture(src, out_dir)
        except Exception as e:
            print(f"skip {name}: {type(e).__name__}: {str(e)[:90]}", file=sys.stderr)
            continue
        print(f"ok   {name}: {len(out[name]['records'])} records")

    with open(os.path.join(out_dir, "expected.json"), "w") as f:
        json.dump(out, f, ensure_ascii=False, indent=1, default=str)
    print(f"\n{len(out)} sources -> {out_dir}", file=sys.stderr)


if __name__ == "__main__":
    main()
