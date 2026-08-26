# proxypool — TODO

## Phase 1 — Sources + dedupe test
- [x] Author `sources.jsonc` with all sources (format, extract, pagination, includes, fallback)
- [x] Build `tests/test_sources.py`:
  - [x] Load `sources.jsonc`
  - [x] Fetch each source (with pagination + fallback)
  - [x] Parse per config (JSON field paths / regex on strings / HTML selectors)
  - [x] Save raw + parsed to `test_output/`
  - [x] Pairwise subset + Jaccard report
- [x] Run test, review report, prune `sources.jsonc` (40 → 29 sources; see comment block in sources.jsonc)

## Phase 2 — Merge + write (no checking)
- [x] `lib/parse.py` — parse dispatcher (JSON/text/HTML) from sources.jsonc
- [x] `lib/util.py` — jsonc loader, normalize, dig, protocol/anon normalization
- [x] `fetch_proxies.py` — load sources, fetch, parse, merge/dedup, write `output/proxies.json` + `output/{http,https,socks4,socks5}.txt`
- [x] Local run, verify output (142139 raw -> 70156 unique; http=62674 https=7808 socks4=20137 socks5=21206)

## Phase 3 — Checker
- [x] `lib/check.py` — async checker (protocols, anonymity, https, RT) with aiohttp
- [x] `lib/geoip.py` — DB-IP Lite MMDB download + lookup
- [x] Calibrate RT baseline (ping test endpoint N times, subtract min)
- [x] Wire checker into `fetch_proxies.py` (skip checks for source-provided fields)
- [x] Local run, verify output (70137 checked -> 2265 alive; http=1952 https=787 socks4=1068 socks5=976)

## Phase 4 — Workflow + docs (gated on user approval)
- [ ] `.github/workflows/fetch.yml` — hourly cron, ubuntu-latest, python 3.11, pip install, run, commit+push as `github-actions[bot]`
- [x] `requirements.txt`
- [ ] `README.md` — what it does, how to use, DB-IP Lite attribution, schedule
- [ ] `AGENT.md` — short, for AI agents
- [ ] Push to GitHub, verify first workflow run

## Done
- [x] Create `proxypool` dir + `git init -b main`
- [x] Write `PLAN.md` and `TODO.md`
