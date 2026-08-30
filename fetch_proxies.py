"""fetch all sources, merge into unified records, write output files.

usage: python3 fetch_proxies.py [--no-check]
outputs:
  output/proxies.json            all records, sorted by response time
  output/{http,https,socks4,socks5}.txt   ip:port lines
"""
import json
import os
import sys
import threading
import time
from collections import deque
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from lib.parse import fetch_source
from lib.util import load_jsonc

ROOT = Path(__file__).resolve().parent
OUT = ROOT / "output"

ANON_RANK = {"": 0, "transparent": 1, "anonymous": 2, "elite": 3}

REQUEUE_COOLDOWN_S = 20
MAX_REQUEUES = 2
GITHUB_WORKERS = 3  # raw.githubusercontent.com rate limits


def _is_github(src):
    from urllib.parse import urlparse
    return urlparse(src.get("url") or "").netloc == "raw.githubusercontent.com"


def fetch_all(sources):
    """fetch every source in parallel; github-raw quarantined to a small pool.
    persistent 429 -> back of the queue with cooldown. watchdog logs sources
    running >8s. returns (records, errors, stats{name: {fetched, requests, elapsed}})"""
    gh = [s for s in sources if _is_github(s)]
    rest = [s for s in sources if not _is_github(s)]
    workers = min(len(rest) or 1, max(16, (os.cpu_count() or 4) * 4))
    progress = {}
    results = {}
    errors = []
    lock = threading.Lock()
    stop = threading.Event()

    def run_pool(pool_sources, workers):
        pending = deque((s, 0, 0.0) for s in pool_sources)  # src, attempts, not_before
        inflight = {}

        def job(src, st):
            # timer starts when a worker actually picks the job up, not at submit
            st.update(start=time.monotonic(), requests=0, page=None, url=None)
            return fetch_source(src, None, 20, st)

        with ThreadPoolExecutor(max_workers=workers) as pool:
            while pending or inflight:
                now = time.monotonic()
                while pending and len(inflight) < workers and pending[0][2] <= now:
                    src, attempts, _ = pending.popleft()
                    st = progress.setdefault(src["name"], {})
                    inflight[pool.submit(job, src, st)] = (src, attempts)
                if not inflight:
                    time.sleep(max(0.1, pending[0][2] - time.monotonic()))
                    continue
                done, _ = wait(inflight, timeout=0.5, return_when=FIRST_COMPLETED)
                for fut in done:
                    src, attempts = inflight.pop(fut)
                    st = progress[src["name"]]
                    elapsed = time.monotonic() - st["start"]
                    try:
                        recs, errs = fut.result()
                    except Exception as e:
                        recs, errs = [], [f"{src['name']}: crashed: {e}"]
                    if any("429" in e for e in errs) and attempts < MAX_REQUEUES:
                        pending.append((src, attempts + 1, time.monotonic() + REQUEUE_COOLDOWN_S))
                        print(f"{src['name']:24} 429 -> requeued ({attempts + 1}/{MAX_REQUEUES})",
                              flush=True)
                        continue
                    with lock:
                        errors.extend(errs)
                        results[src["name"]] = {
                            "recs": recs, "requests": st.get("requests", 0),
                            "elapsed": elapsed, "requeues": attempts,
                        }
                    print(f"{src['name']:24} {len(recs):7} recs  "
                          f"{st.get('requests', 0):4} reqs  {elapsed:5.1f}s", flush=True)

    def watchdog():
        announced = {}
        while not stop.is_set():
            now = time.monotonic()
            for name, st in list(progress.items()):
                if name in results:
                    continue
                elapsed = now - st.get("start", now)
                if elapsed > 8 and now - announced.get(name, 0) >= 8:
                    announced[name] = now
                    print(f"[slow {elapsed:5.0f}s] {name}: reqs={st.get('requests', 0)} "
                          f"page={st.get('page') or '-'} url={st.get('url') or '-'}",
                          flush=True)
            stop.wait(1.0)

    wd = threading.Thread(target=watchdog, daemon=True)
    wd.start()
    threads = [threading.Thread(target=run_pool, args=(rest, workers)),
               threading.Thread(target=run_pool, args=(gh, GITHUB_WORKERS))]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    stop.set()
    wd.join()

    records = [r for s in sources for r in results.get(s["name"], {}).get("recs", [])]
    stats = {n: {"fetched": len(r["recs"]), "requests": r["requests"],
                 "elapsed": r["elapsed"], "requeues": r["requeues"]}
             for n, r in results.items()}
    return records, errors, stats


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
        cur["_provided"] = set(cur.get("_provided", set())) | set(rec.get("_provided", set()))

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
        r["response_time_raw_ms"] = None  # filled by checker phase
    return records


def sort_records(records):
    return sorted(records, key=lambda r: (
        -(r.get("quality") or 0),
        r.get("response_time_ms") is None,
        r.get("response_time_ms") or float("inf"),
    ))


def _source_link_names(sources):
    """home/display fallbacks: github sources get repo links + owner/repo names,
    others get their domain. colliding repos get disambiguated with the file stem."""
    from collections import Counter
    from urllib.parse import urlparse

    def repo_of(url):
        p = urlparse(url or "")
        parts = [x for x in p.path.split("/") if x]
        if p.netloc in ("raw.githubusercontent.com", "github.com"):
            if parts and parts[0] == "wiki":  # wiki repos: /wiki/owner/repo/...
                parts = parts[1:]
            return (parts[0], parts[1]) if len(parts) >= 2 else None
        if p.netloc == "cdn.jsdelivr.net" and parts[:1] == ["gh"]:
            return (parts[1], parts[2]) if len(parts) >= 3 else None
        return None

    repos = {s["name"]: r for s in sources if (r := repo_of(s.get("url")))}
    counts = Counter(repos.values())

    home, display = {}, {}
    for s in sources:
        name = s["name"]
        link = s.get("home")
        label = s.get("display")
        r = repos.get(name)
        if r:
            owner, repo = r
            link = link or f"https://github.com/{owner}/{repo}"
            if not label:
                label = f"{owner}/{repo}"
                if counts[r] > 1:
                    stem = (s.get("url") or "").rsplit("/", 1)[-1].rsplit(".", 1)[0]
                    label = f"{label} ({stem})"
        if not link:
            host = urlparse(s.get("url") or "").netloc.removeprefix("www.")
            link = f"https://{host}" if host else None
            label = label or host or name
        home[name] = link
        display[name] = label
    return home, display


def _html_table(headers, rows):
    cell = 'style="border:1px solid #30363d; padding:3px 8px; text-align:left"'
    th = "".join(f"<th {cell}><b>{h}</b></th>" for h in headers)
    body = "".join(
        "<tr>" + "".join(f"<td {cell}>{c}</td>" for c in row) + "</tr>"
        for row in rows
    )
    return (f'<table style="border-collapse:collapse; font-size:13px">'
            f'<thead><tr>{th}</tr></thead><tbody>{body}</tbody></table>')


def write_outputs(records, fetched_per_source=None, sources=None):
    OUT.mkdir(exist_ok=True)
    carried = {id(r) for r in records if r.get("_carried")}
    for r in records:
        r.pop("_provided", None)
        r.pop("_carried", None)
        r.setdefault("asn", None)
        r.setdefault("as_org", "")
        r.setdefault("ip_type", "")
        r.setdefault("ip_version", "")
    records = sort_records(records)
    (OUT / "proxies.json").write_text(json.dumps(records, indent=2))

    buckets = {
        "http.txt": [], "https.txt": [],
        "socks4.txt": [], "socks5.txt": [], "all.txt": [],
    }
    for r in records:
        ps = sorted(r["protocols"])
        if not ps:
            continue  # no protocol -> can't form ip:port lines
        line = f"{r['ip']}:{r['port']}\n"
        if "http" in ps:
            buckets["http.txt"].append(line)
        if "https" in ps or r["https"]:
            buckets["https.txt"].append(line)
        if "socks4" in ps:
            buckets["socks4.txt"].append(line)
        if "socks5" in ps:
            buckets["socks5.txt"].append(line)
        buckets["all.txt"].append(f"{ps[0]}://{r['ip']}:{r['port']}\n")
    for name, lines in buckets.items():
        (OUT / name).write_text("".join(lines))

    # refresh readme badges
    readme = ROOT / "README.md"
    if readme.exists():
        import re
        total = len(records)
        rts = [r["response_time_ms"] for r in records if r.get("response_time_ms")]
        avg = round(sum(rts) / len(rts)) if rts else 0
        today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        t = readme.read_text()
        t = re.sub(r"total%20proxies-\d+", f"total%20proxies-{total}", t)
        t = re.sub(r"avg%20response-[\d.]+ms", f"avg%20response-{avg}ms", t)
        esc = today.replace("-", "--")
        t = re.sub(r"(last%20check-)[\d-]+?(?=-green)", rf"\g<1>{esc}", t)
        # refresh readme tables between markers (HTML; laid out side-by-side
        # via the flex div wrapping the markers in README.md)
        from collections import Counter

        types = Counter((r.get("ip_type") or "unknown") for r in records)
        type_rows = [[k, v] for k, v in types.most_common()]
        t = re.sub(r"(<!-- types:start -->\n).*?(\n<!-- types:end -->)",
                   rf"\g<1>{_html_table(['type', 'proxies'], type_rows)}\g<2>", t,
                   flags=re.S)

        countries = Counter(r.get("country") or "??" for r in records)
        top = countries.most_common(4)
        crows = [[c, n] for c, n in top]
        other = sum(v for _, v in list(countries.items())[4:])
        if other:
            crows.append(["other", other])
        t = re.sub(r"(<!-- countries:start -->\n).*?(\n<!-- countries:end -->)",
                   rf"\g<1>{_html_table(['country', 'proxies'], crows)}\g<2>", t,
                   flags=re.S)

        # per-source table: sorted by overall quality score. quality blends
        # live success rate, avg response time, and reliability over time.
        if fetched_per_source is not None:
            from lib.history import RT_GOOD_MS, RT_BAD_MS

            def _speed_pct(avg_rt):
                if not avg_rt:
                    return 50
                return round(100 * max(0.0, min(1.0,
                    1.0 - (avg_rt - RT_GOOD_MS) / (RT_BAD_MS - RT_GOOD_MS))))

            def _reliability_pct(rl):
                # rl in [0,1]; None (no history) -> neutral 0.5
                return round(100 * (rl if rl is not None else 0.5))

            agg = {}
            for r in records:
                if id(r) in carried:
                    continue  # recycled proxies aren't fresh source IPs
                rl = r.get("reliability")
                for s in r.get("sources", []):
                    a = agg.setdefault(s, {"alive": 0, "rts": [], "rels": [],
                                           "countries": Counter()})
                    a["alive"] += 1
                    rt = r.get("response_time_ms")
                    if rt:
                        a["rts"].append(rt)
                    a["rels"].append(rl)
                    if r.get("country"):
                        a["countries"][r["country"]] += 1
            home, display = _source_link_names(sources)
            names = list(display) or sorted(fetched_per_source)
            rows = []
            for s in names:
                fetched = fetched_per_source.get(s, 0)
                a = agg.get(s, {"alive": 0, "rts": [], "rels": [],
                                "countries": Counter()})
                alive = a["alive"]
                pct = round(100 * alive / fetched) if fetched else 0
                avg_rt = round(sum(a["rts"]) / len(a["rts"])) if a["rts"] else 0
                rel = (sum(x for x in a["rels"] if x is not None)
                       / len([x for x in a["rels"] if x is not None])
                       if a["rels"] and any(x is not None for x in a["rels"]) else 0.5)
                rel_pct = _reliability_pct(rel if a["rels"] else None)
                speed = _speed_pct(avg_rt)
                quality = round(0.5 * pct + 0.3 * speed + 0.2 * rel_pct)
                top_countries = ", ".join(c for c, _ in a["countries"].most_common(2)) or "?"
                rows.append((quality, alive, s, fetched, pct, rel_pct, f"{avg_rt}ms",
                             top_countries))
            rows.sort(key=lambda x: (x[0], x[1]), reverse=True)
            srows = []
            for i, x in enumerate(rows):
                label = display[x[2]]
                link = home.get(x[2])
                cell = f'<a href="{link}">{label}</a>' if link else label
                if i == 0:  # bold the #1 source
                    cell = f"<b>{cell}</b>"
                srows.append([cell, f"{x[0]}", f"{x[4]}%", f"{x[5]}%", x[6],
                              x[3], x[1], x[7]])
            source_table = _html_table(
                ["source", "quality", "success", "reliability", "avg rt",
                 "fetched", "alive", "top countries"],
                srows)
            t = re.sub(r"<!-- sources:start -->.*?<!-- sources:end -->",
                       f"<!-- sources:start -->\n{source_table}\n<!-- sources:end -->",
                       t, flags=re.S)

        # anonymity share
        anon = Counter((r.get("anonymity") or "unknown") for r in records)
        anon_rows = [[k, v] for k, v in anon.most_common()]
        t = re.sub(r"<!-- anon:start -->.*?<!-- anon:end -->",
                   f"<!-- anon:start -->\n{_html_table(['anonymity', 'proxies'], anon_rows)}\n<!-- anon:end -->",
                   t, flags=re.S)

        # protocol (type) share
        proto = Counter()
        for r in records:
            for p in r.get("protocols", []):
                proto[p] += 1
        proto_rows = [[k, v] for k, v in proto.most_common()]
        t = re.sub(r"<!-- proto:start -->.*?<!-- proto:end -->",
                   f"<!-- proto:start -->\n{_html_table(['type', 'proxies'], proto_rows)}\n<!-- proto:end -->",
                   t, flags=re.S)

        # top 5 ports
        ports = Counter(r["port"] for r in records).most_common(5)
        port_rows = [[p, n] for p, n in ports]
        t = re.sub(r"<!-- ports:start -->.*?<!-- ports:end -->",
                   f"<!-- ports:start -->\n{_html_table(['port', 'proxies'], port_rows)}\n<!-- ports:end -->",
                   t, flags=re.S)

        # final layout: sources table on top, the rest side-by-side in a flex row
        def _block(name):
            m = re.search(rf"<!-- {name}:start -->.*?<!-- {name}:end -->", t, re.S)
            return m.group(0) if m else ""
        _order = ["sources", "types", "countries", "anon", "proto", "ports"]
        _parts = {n: _block(n) for n in _order}
        _flex = ('<div style="display:flex; flex-wrap:wrap; gap:16px; align-items:flex-start">\n\n'
                 + "\n\n".join(_parts[n] for n in ["types", "countries", "anon", "proto", "ports"])
                 + "\n\n</div>")
        _starts = [t.find(f"<!-- {n}:start -->") for n in _order if t.find(f"<!-- {n}:start -->") != -1]
        _ends = [t.find(f"<!-- {n}:end -->") + len(f"<!-- {n}:end -->") for n in _order if t.find(f"<!-- {n}:start -->") != -1]
        if _starts and _ends:
            t = t[:min(_starts)] + _parts["sources"] + "\n\n" + _flex + t[max(_ends):]

        readme.write_text(t)

    return {k: len(v) for k, v in buckets.items()}


def main():
    import argparse
    import asyncio

    ap = argparse.ArgumentParser()
    ap.add_argument("--no-check", action="store_true",
                    help="skip proxy checking (fetch/merge only)")
    ap.add_argument("--concurrency", type=int, default=0,
                    help="override checker concurrency (default: derived from speedtest)")
    ap.add_argument("--no-speedtest", action="store_true",
                    help="skip the bandwidth measurement, use default concurrency")
    args = ap.parse_args()

    cfg = load_jsonc(ROOT / "sources.jsonc")
    sources = cfg["sources"]
    t_run = time.monotonic()

    t0 = time.monotonic()
    all_records, errors, stats = fetch_all(sources)
    fetch_s = time.monotonic() - t0

    print(f"\nfetched {len(all_records)} raw records from {len(sources)} sources "
          f"in {fetch_s:.1f}s")
    slowest = sorted(stats.items(), key=lambda kv: kv[1]["elapsed"], reverse=True)[:10]
    print("slowest sources:")
    for name, st in slowest:
        print(f"  {name:24} {st['elapsed']:5.1f}s  {st['fetched']:7} recs  {st['requests']} reqs")
    records = finalize(merge(all_records))
    print(f"unique proxies: {len(records)}")

    # per-source fetched counts (before dead proxies are dropped) for success rate
    from collections import Counter
    fetched_per_source = Counter()
    for r in records:
        for s in r.get("sources", []):
            fetched_per_source[s] += 1
    source_names = [s["name"] for s in sources]

    # carry over last run's alive proxies so they get re-checked too
    prev_path = OUT / "proxies.json"
    carried = 0
    if prev_path.exists():
        seen = {f"{r['ip']}:{r['port']}" for r in records}
        for r in json.loads(prev_path.read_text()):
            if f"{r['ip']}:{r['port']}" in seen:
                continue
            r.pop("_provided", None)
            r.setdefault("protocols", [])
            r.setdefault("source_meta", {})
            r["_carried"] = True
            records.append(r)
            carried += 1
        print(f"carried over {carried} proxies from previous run")

    if not args.no_check:
        from lib.check import check_all
        from lib.geoip import GeoIP, download_mmdb

        t0 = time.monotonic()
        mmdb = download_mmdb(ROOT / ".cache")
        geo = GeoIP(mmdb)
        filled = 0
        for r in records:
            if not r["country"]:
                c = geo.country(r["ip"])
                if c:
                    r["country"] = c
                    filled += 1
        print(f"geoip filled country for {filled} records in {time.monotonic() - t0:.1f}s")

        # asn + hosting/residential classification for every record
        from lib.geoip import AsnDB, download_asn_categories, download_asn_mmdb

        t0 = time.monotonic()
        categories = download_asn_categories(ROOT / ".cache")
        print(f"ipverse asn categories: {len(categories)} classified")
        asn_db = AsnDB(download_asn_mmdb(ROOT / ".cache"), categories)
        for r in records:
            info = asn_db.lookup(r["ip"])
            r["asn"] = info["asn"]
            r["as_org"] = info["as_org"]
            r["ip_type"] = info["ip_type"]
        print(f"asn/ip_type filled in {time.monotonic() - t0:.1f}s")

        from lib.history import (History, NEW_PROXY_SCORE, skip_keys)

        hist = History(OUT / "history.db")
        state = hist.state_map()
        skips = skip_keys(records, state)
        if skips:
            print(f"skipping {len(skips)} proxies that have been dead "
                  f"(re-checked in ~{24}h)")
        print(f"checking {len(records)} proxies...")
        t0 = time.monotonic()
        prev_alive = {k for k, v in state.items() if v.get("last_ok_ts")}
        records, check_stats, outcomes, _ = asyncio.run(
            check_all(records, skip=skips,
                      concurrency=args.concurrency,
                      speedtest=not args.no_speedtest,
                      prev_alive=prev_alive))
        print(f"alive={check_stats['alive']} dead={check_stats['dead']} "
              f"skipped={check_stats['skipped']} revived={check_stats['revived']} "
              f"baseline={check_stats['baseline_ms']}ms "
              f"in {time.monotonic() - t0:.1f}s")

        t0 = time.monotonic()
        ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        hist.record(outcomes, ts)
        hist.update_state(outcomes, ts, state)
        scores = hist.scores()
        hist.close()
        for r in records:
            s = scores.get((r["ip"], r["port"]))
            if s:
                r.update(s)
            else:
                r.update(reliability=NEW_PROXY_SCORE, quality=NEW_PROXY_SCORE,
                         checks_total=0, checks_ok=0,
                         first_seen=None, last_seen=None)
        print(f"history: {len(scores)} proxies scored in {time.monotonic() - t0:.1f}s")

    t0 = time.monotonic()
    counts = write_outputs(records, fetched_per_source, sources)
    print(f"http={counts['http.txt']} https={counts['https.txt']} "
          f"socks4={counts['socks4.txt']} socks5={counts['socks5.txt']} "
          f"(written in {time.monotonic() - t0:.1f}s)")
    print(f"total elapsed: {time.monotonic() - t_run:.1f}s")
    if errors:
        print("\nerrors:")
        for e in errors:
            print(" ", e)


if __name__ == "__main__":
    main()
