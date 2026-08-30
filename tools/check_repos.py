#!/usr/bin/env python3
"""check repo freshness for sources that pull from github.

usage: python3 check_repos.py [--stale-days 90]

extracts github repos referenced by sources.jsonc urls
(raw.githubusercontent.com, github.com, cdn.jsdelivr.net/gh/),
fetches each repo's latest commit date, and prints a table.
optionally writes the repos whose last commit is older than
--stale-days to stdout as a machine-readable list.

exit code 0 always; data on stdout.
"""
import json
import re
import sys
import time
from datetime import datetime, timezone
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

from lib.util import load_jsonc

PATTERNS = (
    re.compile(r"raw\.githubusercontent\.com/([^/]+)/([^/]+)/([^/]+)/(.+?)(?:[?#].*)?$"),
    re.compile(r"cdn\.jsdelivr\.net/gh/([^/]+)/([^/@]+)(?:@([^/]+))?/(.+?)(?:[?#].*)?$"),
    re.compile(r"github\.com/([^/]+)/([^/]+)/(?:raw|blob)/([^/]+)/(.+?)(?:[?#].*)?$"),
)


def repo_of(url):
    for pat in PATTERNS:
        m = pat.search(url or "")
        if m:
            return {"owner": m.group(1), "repo": m.group(2), "ref": m.group(3), "path": m.group(4)}
    return None


def latest_commit(owner, repo, path=None):
    """latest commit date for a repo (or a path within it). returns datetime or None."""
    url = f"https://api.github.com/repos/{owner}/{repo}/commits?per_page=1"
    if path:
        url += f"&path={path}"
    req = Request(url, headers={
        "Accept": "application/vnd.github+json",
        "User-Agent": "proxypool-repo-checker",
    })
    try:
        with urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read())
    except HTTPError as e:
        return {"error": f"http {e.code}"}
    except (URLError, TimeoutError) as e:
        return {"error": str(e)}
    if not data:
        return {"error": "no commits"}
    dt = data[0]["commit"]["committer"]["date"]
    return {"date": datetime.fromisoformat(dt.replace("Z", "+00:00"))}


def main():
    stale_days = 90
    if "--stale-days" in sys.argv:
        stale_days = int(sys.argv[sys.argv.index("--stale-days") + 1])

    cfg = load_jsonc("sources.jsonc")
    sources = cfg["sources"] if isinstance(cfg, dict) else cfg

    repos = {}
    for s in sources:
        url = s.get("url") or s.get("home") or ""
        r = repo_of(url)
        if not r:
            continue
        key = (r["owner"].lower(), r["repo"].lower())
        repos.setdefault(key, {"ref": r, "sources": []})
        repos[key]["sources"].append(s["name"])

    now = datetime.now(timezone.utc)
    rows = []
    for (owner, repo), info in sorted(repos.items()):
        res = latest_commit(owner, repo, info["ref"]["path"])
        if "error" in res:
            # retry without path (file may have been renamed)
            res = latest_commit(owner, repo)
        rows.append((owner, repo, res, info["sources"]))
        time.sleep(1)  # be polite to the rate limit

    stale = []
    print(f"{'repo':44} {'last commit':20} {'age':>7}  sources")
    print("-" * 100)
    for owner, repo, res, names in rows:
        if "error" in res:
            print(f"{owner}/{repo:33} {'ERROR: ' + res['error']:20} {'?':>7}  {', '.join(names)}")
            stale.append(f"{owner}/{repo}")
            continue
        age = (now - res["date"]).days
        flag = " STALE" if age > stale_days else ""
        print(f"{owner}/{repo:33} {res['date']:%Y-%m-%d %H:%M}   {age:>5}d  {', '.join(names)}{flag}")
        if age > stale_days:
            stale.append(f"{owner}/{repo}")

    if "--json" in sys.argv:
        print("\nstale repos:", json.dumps(stale))


if __name__ == "__main__":
    main()
