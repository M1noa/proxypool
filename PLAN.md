# proxypool — Plan

Ultimate free proxy list repo. GitHub Actions fetches ~40 sources hourly, parses them via a single declarative `sources.jsonc` config (no per-source code), merges/dedupes, checks liveness/protocols/anonymity/HTTPS/response-time, looks up country offline via DB-IP Lite MMDB, and commits `output/{http,https,socks4,socks5}.txt` + `output/proxies.json` (sorted by response time).

## Decisions

- **Repo:** `M1noa/proxypool` on GitHub only (no GitLab mirror — exception to AGENTS.md, noted in AGENT.md). Public. Default branch `main`.
- **Schedule:** `cron: '0 * * * *'` (hourly) + `workflow_dispatch`. Public repo = unlimited Actions minutes.
- **GeoIP:** DB-IP Lite MMDB (~2MB, free, CC-BY 4.0, no account/secrets). Downloaded fresh each run.
- **http.txt:** all HTTP-capable proxies (incl. those that also support HTTPS CONNECT). `protocols` array on each JSON record captures multi-protocol support.
- **No external API for country** — MMDB lookup only.
- **Per-source code is forbidden.** All parsing logic is driven by `sources.jsonc`: format, extract (field paths / regex / CSS selectors), pagination (`page` or `offset` placeholder, start/step/stop), headers, body, fallback_url, `includes` (which metadata fields the source already provides → skip those checks downstream).
- **Strip protocol prefix** if present in raw text (`socks5://1.2.3.4:1080` → ip=1.2.3.4, port=1080, protocol=socks5).
- **Skip checks** for any field a source already provides (country/anonymity/https/protocols) — only run checks for missing fields. **Preserve all source metadata** (city, gateway, uptime, last_checked, response_time) in the merged record.
- **Calibrate response time:** ping the test endpoint directly N times before checking, take the shortest as baseline, subtract from each proxy's measured RT.

## Layout

```
proxypool/
├── .github/workflows/
│   └── fetch.yml              # hourly cron
├── sources.jsonc              # source definitions — single source of truth
├── fetch_proxies.py           # parse sources → fetch → merge → check → write
├── lib/
│   ├── parse.py               # JSON / text / HTML parsers driven by sources.jsonc
│   ├── check.py               # async checker: protocols, anonymity, https, RT
│   ├── geoip.py               # MMDB download + lookup
│   └── util.py                # http client, normalization, merge logic
├── tests/
│   └── test_sources.py        # Phase 1: fetch all, parse, dedupe report
├── output/                    # generated; committed by the workflow
│   ├── http.txt
│   ├── https.txt
│   ├── socks4.txt
│   ├── socks5.txt
│   └── proxies.json
├── requirements.txt
├── README.md
├── AGENT.md
├── PLAN.md
└── TODO.md
```

## Unified record schema (`output/proxies.json`)

Top-level: `{ "updated": "ISO-8601", "count": N, "proxies": [ ... ] }`, sorted by `response_time_ms` ascending (missing/null RT → end of list).

Each proxy:
```json
{
  "ip": "1.2.3.4",
  "port": 8080,
  "protocols": ["http","https"],
  "country": "DE",
  "anonymity": "elite",
  "https": true,
  "response_time_ms": 123.4,
  "sources": ["proxifly","geonode"],
  "last_checked": "2026-08-19T06:00:00Z",
  "source_meta": { ... }   // optional preserved extras (city, gateway, uptime, etc.)
}
```

Field semantics:
- `protocols`: array of `http|https|socks4|socks5` the host speaks. `https` here means HTTPS CONNECT tunneling, not "the HTTP proxy also serves HTTPS origins".
- `country`: ISO-3166 alpha-2, `""` if unknown.
- `anonymity`: `transparent|anonymous|elite|""` (unknown = empty, never guess).
- `https`: bool — does the proxy support HTTPS CONNECT?
- `response_time_ms`: calibrated (baseline subtracted). `null` if not checked / failed.
- `sources`: every source that contributed this proxy.
- `source_meta`: merged extras (e.g. city from geonode, gateway from proxydb, uptime from proxydb). Freeform per-field; later sources don't overwrite earlier non-empty values.

## txt files

One `ip:port` per line. `http.txt` = all records with `"http"` in protocols. `https.txt` = all with `"https"`. `socks4.txt` / `socks5.txt` likewise. Sorted by `response_time_ms` to match the JSON.

## Merge / dedup

- Key = `ip:port`.
- `protocols`: union across sources.
- `anonymity`: prefer most-anonymous known (`elite > anonymous > transparent > ""`).
- `country`: first non-empty (prefer MMDB-confirmed over source-claimed, but source-claimed is the fallback if MMDB check is skipped).
- `https`: true if any source says true, or our own check confirms.
- `response_time_ms`: from our own check (overwrites source value), `null` if not checked.
- `sources`: union.
- `source_meta`: merge field-by-field, first non-empty wins.

## Checker (async, ~256–512 concurrent, 8s timeout per attempt)

1. **Protocol detection** — for each unknown-protocol proxy, try HTTP GET, HTTPS CONNECT, SOCKS4 handshake, SOCKS5 handshake in parallel against a test endpoint (e.g. `http://httpbin.org/ip` or self-hosted echo). Record all that succeed in `protocols`.
2. **Anonymity** — HTTP via proxy to a header-echo endpoint; inspect `Via`/`X-Forwarded-For`/`Forwarded`:
   - `transparent`: real client IP visible
   - `anonymous`: proxy headers present, real IP hidden
   - `elite`: no proxy headers
3. **HTTPS** — try HTTPS request through proxy (CONNECT tunnel).
4. **Response time** — baseline: hit the test endpoint directly N times (N=3–5), take min. Measured: time the proxy request. `rt = measured - baseline`. Negative → 0.
5. **Country** — MMDB lookup only, no API.

## Phases

1. **sources.jsonc + test_sources.py** — fetch every source, parse per config, save raw+parsed, print dedupe report (per-source count, subset flags, Jaccard similarity, drop/keep recommendation). Prune `sources.jsonc` based on results. *(local)*
2. **fetch_proxies.py** — parse + merge + dedup + write JSON+txt, no checking. *(local)*
3. **Async checker** — protocols, anonymity, https, response time, geoip. *(local)*
4. **Workflow + push** — GitHub Actions hourly cron, README, AGENT.md. Wait for user approval before pushing.

## Sources (Phase 1 — will prune after dedupe test)

See `sources.jsonc` for the full annotated list. Categories:
- **JSON object-per-proxy:** proxifly, geonode, geonix, roundproxies, proxiware, sunny9577
- **JSON of strings:** proxyscrape (with jsdelivr fallback)
- **Text (ip:port or proto://ip:port per line):** TheSpeedX (3 files), iplocate (all + 4 protocols), roosterkid (3), Anonym0usWork1221 (4), ProxyScraper (3), Argh94 (4), zloi-user/hideip.me (4), hookzof (socks5), ErcinDedeoglu (4), elliottophellia (1, inspect)
- **HTML tables:** free-proxy-list.net (single page), proxydb.net (offset+30 paged), advanced.name (page paged until empty)
