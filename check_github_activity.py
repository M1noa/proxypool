#!/usr/bin/env python3
import argparse
import datetime as dt
import json
import os
import sys
from urllib.parse import urlparse

import requests


def extract_repo(url):
    u = urlparse(url.strip())
    host = u.netloc.lower()
    parts = [p for p in u.path.split("/") if p]

    if host == "raw.githubusercontent.com":
        # raw.githubusercontent.com/wiki/owner/repo/...
        if parts and parts[0] == "wiki" and len(parts) >= 3:
            return f"{parts[1]}/{parts[2]}"
        # raw.githubusercontent.com/owner/repo/ref/...
        if len(parts) >= 2:
            return f"{parts[0]}/{parts[1]}"

    if host in {"github.com", "www.github.com"} and len(parts) >= 2:
        repo = parts[1][:-4] if parts[1].endswith(".git") else parts[1]
        return f"{parts[0]}/{repo}"

    if host == "api.github.com" and len(parts) >= 3 and parts[0] == "repos":
        return f"{parts[1]}/{parts[2]}"

    if host == "cdn.jsdelivr.net" and len(parts) >= 3 and parts[0] == "gh":
        return f"{parts[1]}/{parts[2].split('@', 1)[0]}"

    if host.endswith(".github.io") and parts:
        return f"{host[:-10]}/{parts[0]}"

    return None


def last_commit(repo, timeout=15):
    headers = {"Accept": "application/vnd.github+json"}
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        headers["Authorization"] = f"Bearer {token}"

    r = requests.get(
        f"https://api.github.com/repos/{repo}/commits",
        params={"per_page": 1},
        headers=headers,
        timeout=timeout,
    )
    if r.status_code != 200:
        return None, f"github api {r.status_code}: {r.text[:120]}"

    items = r.json()
    if not items:
        return None, "repo has no commits"

    commit = items[0].get("commit", {})
    raw = (commit.get("committer") or commit.get("author") or {}).get("date")
    if not raw:
        return None, "commit has no date"

    when = dt.datetime.fromisoformat(raw.replace("Z", "+00:00"))
    return when, None


def main():
    ap = argparse.ArgumentParser(description="check whether github repos have committed recently")
    ap.add_argument("urls", nargs="*", help="github urls (repo, raw, wiki, jsdelivr, github.io)")
    ap.add_argument("-f", "--file", help="read urls from a file")
    ap.add_argument("-d", "--days", type=float, default=2.0, help="max allowed age in days (default: 2)")
    ap.add_argument("--json", action="store_true", help="emit json lines")
    args = ap.parse_args()

    urls = list(args.urls)
    if args.file:
        with open(args.file, encoding="utf-8") as fh:
            urls.extend(line.strip() for line in fh if line.strip())

    if not urls:
        ap.error("provide at least one url")

    now = dt.datetime.now(dt.timezone.utc)
    bad = 0
    seen = set()

    for url in urls:
        repo = extract_repo(url)
        if not repo:
            row = {"status": "SKIP", "url": url, "reason": "not a github repo url"}
            print(json.dumps(row) if args.json else f"SKIP {url} (not a github repo url)")
            continue

        key = repo.lower()
        if key in seen:
            continue
        seen.add(key)

        when, err = last_commit(repo)
        if err:
            bad += 1
            row = {"status": "FAIL", "repo": repo, "url": url, "reason": err}
            print(json.dumps(row) if args.json else f"FAIL {repo} ({err})")
            continue

        age = now - when
        ok = age <= dt.timedelta(days=args.days)
        if not ok:
            bad += 1

        row = {
            "status": "PASS" if ok else "FAIL",
            "repo": repo,
            "last_commit": when.isoformat(),
            "age_hours": round(age.total_seconds() / 3600, 2),
            "max_age_days": args.days,
            "url": url,
        }
        if args.json:
            print(json.dumps(row))
        else:
            print(f"{row['status']} {repo} last_commit={row['last_commit']} age={row['age_hours']}h")

    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
