# Proxy Sources — Endpoint Reference

All sources with **observed schemas** (probed 2026-08-25 via `tests/probe_sources.py`; raw dumps in `test_output/raw/`).

Formats:
- **text** — one proxy per line (`ip:port`, optional `proto://` prefix)
- **json** — JSON document
- **html** — HTML page requiring table extraction

---

## GitHub-hosted sources

### proxifly/free-proxy-list
- Repo: <https://github.com/proxifly/free-proxy-list> · Path: `proxies/all/data.json`
- Fetch: `https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/all/data.json`
- Observed schema: array of objects —
  ```json
  {"proxy":"socks5://208.102.51.6:58208","protocol":"socks5","ip":"...","port":58208,
   "https":false,"anonymity":"transparent","score":1,"geolocation":{"country":"US","city":""}}
  ```
- Richest metadata source. Provides: protocol, country code, anonymity, https.

### TheSpeedX/SOCKS-List
- Repo: <https://github.com/TheSpeedX/SOCKS-List>
- Raw files (`master/`): `http.txt`, `socks4.txt`, `socks5.txt`
- Observed schema: text, bare `ip:port`, protocol implied by file.

### iplocate/free-proxy-list
- Repo: <https://github.com/iplocate/free-proxy-list> (branch `main`)
- Files / raw URLs:
  - `all-proxies.txt` → `.../refs/heads/main/all-proxies.txt` — mixed, inline `proto://ip:port`
  - `protocols/http.txt`, `protocols/https.txt` (**404 — dropped**), `protocols/socks4.txt`, `protocols/socks5.txt`
- Observed schema: text; per-protocol files bare `ip:port`.

### roosterkid/openproxylist
- Repo: <https://github.com/roosterkid/openproxylist>
- Files: `HTTPS_RAW.txt`, `SOCKS4_RAW.txt`, `SOCKS5_RAW.txt` (branch `main`)
- Observed schema: text, bare `ip:port`. HTTPS list is tiny (~40 lines).

### Anonym0usWork1221/Free-Proxies
- Repo: <https://github.com/Anonym0usWork1221/Free-Proxies>
- Files: `proxy_files/{http,https,socks4,socks5}_proxies.txt` (branch `main`)
- Observed schema: text, bare `ip:port`.

### elliottophellia/proxylist
- Repo: <https://github.com/elliottophellia/proxylist>
- File: `results/pmix_checked.txt` (branch `master`)
- Observed schema: text, mixed protocols with inline prefix (`http://ip:port`, `socks5://ip:port`).

### ProxyScraper/ProxyScraper
- Repo: <https://github.com/ProxyScraper/ProxyScraper>
- Files: `http.txt`, `socks4.txt`, `socks5.txt` (branch `main`)
- Observed schema: text, bare `ip:port`.
- ⚠️ Byte-identical to TheSpeedX lists in probe — dedupe candidate.

### Argh94/Proxy-List
- Repo: <https://github.com/Argh94/Proxy-List>
- Files: `HTTP.txt`, `SOCKS4.txt`, `SOCKS5.txt` (branch `main`). ~~`HTTPS.txt`~~ **dropped: contains Telegram MTProto proxies (`t.me/proxy?server=...`), not HTTP proxies.**
- Observed schema: text with inline prefix (`http://ip:port`, `socks5://ip:port`).

### zloi-user/hideip.me
- Repo: <https://github.com/zloi-user/hideip.me>
- Files: `http.txt`, `https.txt`, `socks4.txt`, `socks5.txt` (branch `main`)
- Observed schema: text, `ip:port:COUNTRY NAME` (e.g. `200.80.227.234:4145:Argentina`) — country name included, needs name→code mapping.

### hookzof/socks5_list
- Repo: <https://github.com/hookzof/socks5_list>
- File: `master/proxy.txt`
- Observed schema: text, bare `ip:port`, socks5 only, actively checked.

### ErcinDedeoglu/proxies
- Repo: <https://github.com/ErcinDedeoglu/proxies>
- Files: `proxies/{http,https,socks4,socks5}.txt`
- Observed schema: text, bare `ip:port`.
- ⚠️ Massive aggregator (~1MB http list); head identical to TheSpeedX/ProxyScraper — likely repackaged aggregate, dedupe candidate.

### proxyscrape/free-proxy-list (fallback mirror)
- Repo: <https://github.com/proxyscrape/free-proxy-list> · Path: `proxies/all/data.json`
- Fetch: `https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json`
- Role: fallback only when live API below fails.

### sunny9577/proxy-scraper (GitHub Pages)
- Repo: <https://github.com/sunny9577/proxy-scraper>
- Fetch: `https://sunny9577.github.io/proxy-scraper/proxies.json`
- Observed schema: array of objects —
  ```json
  {"ip":"173.212.245.136","port":"8888","country":"France - Lauterbourg",
   "anonymity":"Elite","type":"HTTP/HTTPS"}
  ```
  Port is a string. `type` may be `"HTTP/HTTPS"` (split on `/`) or `"http"`/`"socks4"`. Country is full name (sometimes "Country - City").

---

## Standalone HTTP endpoints

### ProxyScrape API
- URL: `https://api.proxyscrape.com/v4/free-proxy-list/get?request=get_proxies&proxy_format=protocolipport&format=json`
- Fallback: proxyscrape jsdelivr mirror above.
- Observed schema: object — `{shown_records,total_records,nextpage,proxies:[...]}` where each item:
  ```json
  {"alive":true,"anonymity":"elite","average_timeout":1719.46,
   "ip_data":{"countryCode":"FR","country":"France","city":"Strasbourg","isp":"OVH SAS",...},
   "port":8080,"protocol":"http","ssl":false,"timeout":19862.77,
   "times_alive":17088,"times_dead":598882,"uptime":2.77,"ip":"77.46.138.233"}
  ```
  NOT string items as originally assumed. Provides country code, anonymity, ssl(=https), uptime.

### GeoNode API
- URL template: `https://proxylist.geonode.com/api/proxy-list?page={page}&limit=500&sort_by=responseTime&sort_type=asc`
- Pagination: page-based until empty (`total:2941`, so ~6 pages).
- Observed schema: `{data:[{ip,port:"1080",protocols:["socks5"],anonymityLevel:"elite",asn,city,country:"VN",google,isp,lastChecked,latency,org,speed,upTime,responseTime,...}],total,page,limit}`. Country is ISO code. Rich metadata.

### RoundProxies API
- URL template: `https://roundproxies.com/api/get-free-proxies/?limit=500&page={page}&sort_by=lastChecked&sort_type=desc`
- Pagination: page-based until empty (`total:2942`).
- Observed schema: **identical shape to GeoNode** (`data[]` with same field names). ⚠️ Likely GeoNode clone/mirror — dedupe candidate.

### free-proxy-list.net
- URL: `https://free-proxy-list.net/en/`
- Format: html — table `.fpl-list table tbody tr`, columns: IP Address, Port, Code (country), Country, Anonymity, Google, Https (yes/no), Last Checked.

### Proxiware API
- URL template: `https://papi.proxiware.com/proxies?page={page}`
- Pagination: page-based until empty.
- Observed schema: object —
  ```json
  {"last_updated_minutes":8,"proxies":[{"addr":"147.45.221.111","port":1082,
    "protocol":"https","country":"Albania","country_code":"AL","anonymity":"Elite",
    "speed_ms":79,"created_at":...,"updated_at":...}],
   "total_countries":110,"total_proxies":28482}
  ```
  IP field is `addr` (not `ip`). No pagination metadata in response — stop on empty `proxies`.

### proxydb.net
- URL template: `https://proxydb.net/?offset={offset}` — 30/page, offset pagination.
- Format: html — table columns: IP, Port, Type, Country (`<abbr title="name city isp">TT</abbr>`), Anonymity ("High Anonymous"), ø Uptime (%), ø R-Time (`0.7s`), Gateway, Checked.

### Nodemaven Free Proxies
- URL: `https://freeproxies.nodemaven.com/proxies?per_page=1000000`
- Observed schema: `{page,per_page,proxies:[{country:"United States" (name),created_at,google,id,ip_address,last_checked,latency,port:"4145",protocol:"SOCKS4",proxy,response,source_name,type:"Anonymous",updated_at}],total}`
  - IP field is `ip_address`; anonymity field is `type`.
  - ⚠️ **`source_name` exposes upstream sources it scrapes** (e.g. `https://raw.githubusercontent.com/casa-ls/proxy-list/main/socks4`, `fyvri/fresh-proxy-list/...`) — confirmed aggregator; useful evidence for dedupe report.

### Geonix API — ❌ DROPPED
- URL: `https://free.geonix.com/api/front/main/pagination/filtration` (POST, `content-type: application/json`, body `{"page":0,"size":1000000,...}`)
- Observed schema: `{totalElements,totalPages,content:[{id,ip,portImageUrl,country (name),proxyType:"SOCKS5",anonymity:"pr-proz.txt"/"el-elit.txt",delay,lastCheckInMinutes,mainProxyState}]}`
- **No port numbers** — ports rendered as hashed image URLs only. Unusable.

### advanced.name
- URL template: `https://advanced.name/freeproxy?page={page}`
- Format: html — paginated table; column mapping to verify during parse phase.

---

## Probe summary

| Status | Sources |
|---|---|
| OK | 37 |
| Dropped | geonix (no ports), argh94-https (MTProto), iplocate-https (404) |
| Suspected duplicates | ProxyScraper ≈ TheSpeedX; ErcinDedeoglu = aggregator; RoundProxies ≈ GeoNode; Nodemaven = GitHub aggregator (proven via source_name) |

Full subset/Jaccard dedupe analysis happens in `tests/test_sources.py` (Phase 1).
