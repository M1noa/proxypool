# proxypool — TODO

## Phase 1 — Sources + dedupe test
- [ ] Author `sources.jsonc` with all ~40 sources (format, extract, pagination, includes, fallback)
- [ ] Build `tests/test_sources.py`:
  - [ ] Load `sources.jsonc`
  - [ ] Fetch each source (with pagination + fallback)
  - [ ] Parse per config (JSON field paths / regex on strings / HTML selectors)
  - [ ] Save raw + parsed to `test_output/<name>.{txt,json}`
  - [ ] Per-source report: count, protocols seen, metadata fields provided, sample, fetch errors
  - [ ] Pairwise subset + Jaccard report
  - [ ] Drop/keep recommendation
- [ ] Run test, review report, prune `sources.jsonc`

## Phase 2 — Merge + write (no checking)
- [ ] `lib/parse.py` — parse dispatcher (JSON/text/HTML) from sources.jsonc
- [ ] `lib/util.py` — normalize, strip `proto://` prefix, merge records
- [ ] `fetch_proxies.py` — load sources, fetch, parse, merge/dedup, write `output/proxies.json` + `output/{http,https,socks4,socks5}.txt`
- [ ] Local run, verify output

## Phase 3 — Checker
- [ ] `lib/check.py` — async checker (protocols, anonymity, https, RT) with aiohttp/httpx
- [ ] `lib/geoip.py` — DB-IP Lite MMDB download + lookup
- [ ] Calibrate RT baseline (ping test endpoint N times, subtract min)
- [ ] Wire checker into `fetch_proxies.py` (skip checks for source-provided fields)
- [ ] Local run, verify output

## Phase 4 — Workflow + docs (gated on user approval)
- [ ] `.github/workflows/fetch.yml` — hourly cron, ubuntu-latest, python 3.11, pip install, run, commit+push as `github-actions[bot]`
- [ ] `requirements.txt`
- [ ] `README.md` — what it does, how to use, DB-IP Lite attribution, schedule
- [ ] `AGENT.md` — short, for AI agents
- [ ] Push to GitHub, verify first workflow run

## Done
- [x] Create `proxypool` dir + `git init -b main`
- [x] Write `PLAN.md` and `TODO.md`
