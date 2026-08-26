# AGENT.md

Config-driven proxy scraper. Hourly GitHub Actions run fetches 29 sources, merges by ip:port, live-checks, commits output/.

## Setup

```bash
pip install -r requirements.txt
python3 fetch_proxies.py            # full run (~30 min)
python3 fetch_proxies.py --no-check # skip checking
```

## Architecture

- `sources.jsonc` — ALL source definitions (url/format/extract/pagination/fallback/includes). Adding a source = adding config, never code.
- `lib/parse.py` — fetch + parse dispatcher (json dotted paths / text regex named groups / html selectors)
- `lib/check.py` — async aiohttp checker: protocol handshakes, header-echo anonymity, baseline-calibrated RT
- `lib/geoip.py` — DB-IP Lite MMDB download + country lookup
- `fetch_proxies.py` — orchestrator; writes output/proxies.json + 4 txt buckets
- `tests/test_sources.py` — fetch/parse all sources + pairwise subset/jaccard dedupe report

## Gotchas

- output/, test_output/, .cache/ are gitignored except workflow commits only output/
- DB-IP URL must end .mmdb.gz (plain .mmdb 404s); falls back to previous month
- "includes" in a source entry means those fields are trusted and skipped during check
- proxydb rate-limits at ~870 records via pagination — accepted
- editing sources.jsonc programmatically: use line-based edits (regexes contain braces)
