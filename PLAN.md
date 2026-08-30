# plan: fetch pipeline overhaul

python first. go rewrite later, once this is the frozen, tested spec.

## 1. workflow (`.github/workflows/fetch.yml`)
queued runs were failing: stale SHA checkout + `git pull --rebase` conflicted on
regenerated files, and bare `git push` is broken from detached HEAD.

- `git pull --rebase -X ours origin main` (outputs are fully regenerated; ours always wins)
- `git push origin HEAD:main`
- drop dead `git add -f history.db` line (covered by `output/`)
- keep `concurrency` group (serialize, no overlap)

## 2. flows -> config (keep `lib/flows.py`, migrate what's clean)

migrate:
- **iproyal** — headers + json + pagination, no engine changes
- **proxyshare** — adds two small generic features: `{path, map}` extract fields, `pages_path` pagination
- **myproxy** — `urls: [{url, set}]` entries; pagination applies to entries containing `{page}`

keep as flows: sixsixdaili (12-combo matrix is uglier in jsonc), proxyhub (link
discovery is a one-source snowflake), proxynova (fix later), spysone (commented
out in sources.jsonc — cloudflare pow challenge).

goodips: api (`api.goodips.com`) was connection-reset dead at time of adding;
add as html source scraping `www.goodips.com/?page={page}&size=15000` tables
(`div.table-list ul > li`), with a comment saying the api is preferred.
delete `_goodips` from flows.py.

## 3. fetch orchestration
- 12s per-request timeout (default), 80s per-source budget with log line when hit
- parallel: github-raw sources quarantined to a 3-worker pool; rest in main pool
  (`min(n, max(16, cpu*4))`, io-bound)
- 429 after retries -> back of queue, 20s cooldown, max 2 requeues
- watchdog: source running >8s logs page / requests / elapsed / last url
- per-source finish lines (records, requests, elapsed) + per-phase elapsed +
  slowest-10 summary

## 4. checker
- eta from cumulative average rate (`remaining / (checked/elapsed)`), not last-10s
- shuffle records before checking (smooths rate variance -> steadier eta)
- multithreaded speedtest during calibrate (4 parallel range gets, cloudflare `__down`)
- concurrency derived from measured bandwidth: `clamp(mbps * 2, 128, 4096)`,
  `--concurrency` / `--no-speedtest` overrides; no hard bandwidth cap
- progress line shows concurrency + measured mbps; psutil added to requirements

## 5. housekeeping
- delete AGENTS.md
- README line 2: agentic-ai notice
- requirements.txt: + psutil

## commits
one per phase, no push.
