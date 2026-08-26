# proxypool

Hourly-refreshed proxy lists. A config-driven scraper fetches proxy lists from 29 public sources, merges and dedupes them, then live-checks every proxy (protocol, HTTPS support, anonymity, country, response time) before publishing.

## Files

- [`output/proxies.json`](output/proxies.json) — all alive proxies with full metadata, sorted by response time (fastest first)
- [`output/http.txt`](output/http.txt) / [`https.txt`](output/https.txt) / [`socks4.txt`](output/socks4.txt) / [`socks5.txt`](output/socks5.txt) — `ip:port` per line, same order

## Record schema

```json
{
  "ip": "1.2.3.4",
  "port": 8080,
  "protocols": ["http", "https"],
  "country": "US",
  "anonymity": "elite",
  "https": true,
  "response_time_ms": 142,
  "sources": ["geonode", "proxifly"],
  "last_checked": "2026-08-25T12:00:00+00:00",
  "source_meta": { "city": "...", "isp": "..." }
}
```

`response_time_ms` is baseline-calibrated: the checker pings the test endpoint directly first and subtracts the minimum round-trip from every measurement. Dead proxies are dropped entirely.

## How it works

Every hour (GitHub Actions, [fetch.yml](.github/workflows/fetch.yml)):

1. Fetch all sources defined in [`sources.jsonc`](sources.jsonc) — a single JSONC config (URL, format, extraction rules, pagination, fallbacks). No per-source code.
2. Merge by `ip:port`: union protocols/sources, strongest anonymity wins, extra metadata (city, ISP, uptime...) preserved in `source_meta`.
3. Fill missing countries from the DB-IP Country Lite MMDB (downloaded each run, no API keys).
4. Async-check up to 512 proxies concurrently: HTTP/HTTPS CONNECT + SOCKS4/SOCKS5 handshakes, header-echo for anonymity, calibrated response time.
5. Write outputs and commit them to this repo.

Fields a source already provides are not re-checked (`includes` in sources.jsonc) to save compute.

## Run locally

```bash
pip install -r requirements.txt
python3 fetch_proxies.py          # full run: fetch + check (~30 min)
python3 fetch_proxies.py --no-check   # fetch + merge only, writes unverified lists
```

## Adding a source

Add an entry to `sources.jsonc`. JSON sources use dotted field paths, text sources use regexes with named groups (`proto`/`ip`/`port`), HTML sources use CSS selectors. See existing entries and [ENDPOINTS.md](ENDPOINTS.md) for upstream formats.

## Attribution

Country data from [DB-IP Country Lite](https://db-ip.com) (CC BY 4.0).
