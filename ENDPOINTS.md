# Proxy Sources — Endpoint Reference

All sources from initial research. Two sections: GitHub-hosted files, and standalone HTTP endpoints.

Formats used throughout:
- **text** — one proxy per line (`ip:port` or `proto://ip:port`)
- **json** — JSON document (object array, string array, or nested)
- **html** — HTML page requiring table extraction

---

## GitHub-hosted sources

### proxifly/free-proxy-list
- Repo: <https://github.com/proxifly/free-proxy-list>
- Path: `proxies/all/data.json`
- Fetch URL (jsdelivr CDN): `https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/all/data.json`
- Format: json — array of objects, richest metadata of all sources (ip, port, protocol(s), country code, anonymity, https support, etc.)
- Notes: primary "ultimate format" reference source.

### TheSpeedX/SOCKS-List
- Repo: <https://github.com/TheSpeedX/SOCKS-List>
- Files / raw URLs:
  - `http.txt` → `https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt`
  - `socks4.txt` → `https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/socks4.txt`
  - `socks5.txt` → `https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/socks5.txt`
- Format: text — bare `ip:port`, protocol implied by which file it came from.
- Notes: no metadata; needs full checking.

### iplocate/free-proxy-list
- Repo: <https://github.com/iplocate/free-proxy-list>
- Files / raw URLs:
  - `all-proxies.txt` → `https://raw.githubusercontent.com/iplocate/free-proxy-list/refs/heads/main/all-proxies.txt` — mixed list, proxies carry their protocol prefix (`socks5://1.2.3.4:1080`)
  - `protocols/http.txt` → `https://raw.githubusercontent.com/iplocate/free-proxy-list/refs/heads/main/protocols/http.txt`
  - `protocols/https.txt` → `https://raw.githubusercontent.com/iplocate/free-proxy-list/refs/heads/main/protocols/https.txt`
  - `protocols/socks4.txt` → `https://raw.githubusercontent.com/iplocate/free-proxy-list/refs/heads/main/protocols/socks4.txt`
  - `protocols/socks5.txt` → `https://raw.githubusercontent.com/iplocate/free-proxy-list/refs/heads/main/protocols/socks5.txt`
- Format: text — `all-proxies.txt` has inline protocol prefix; per-protocol files are bare `ip:port`.
- Suspected duplicate of: its own `all-proxies.txt` is likely a superset of the four `protocols/*` files.

### roosterkid/openproxylist
- Repo: <https://github.com/roosterkid/openproxylist>
- Files / raw URLs:
  - `HTTPS_RAW.txt` → `https://raw.githubusercontent.com/roosterkid/openproxylist/refs/heads/main/HTTPS_RAW.txt`
  - `SOCKS4_RAW.txt` → `https://raw.githubusercontent.com/roosterkid/openproxylist/refs/heads/main/SOCKS4_RAW.txt`
  - `SOCKS5_RAW.txt` → `https://raw.githubusercontent.com/roosterkid/openproxylist/refs/heads/main/SOCKS5_RAW.txt`
- Format: text — bare `ip:port`, protocol implied by file.

### Anonym0usWork1221/Free-Proxies
- Repo: <https://github.com/Anonym0usWork1221/Free-Proxies>
- Files / raw URLs:
  - `proxy_files/http_proxies.txt` → `https://raw.githubusercontent.com/Anonym0usWork1221/Free-Proxies/refs/heads/main/proxy_files/http_proxies.txt`
  - `proxy_files/https_proxies.txt` → `https://raw.githubusercontent.com/Anonym0usWork1221/Free-Proxies/refs/heads/main/proxy_files/https_proxies.txt`
  - `proxy_files/socks4_proxies.txt` → `https://raw.githubusercontent.com/Anonym0usWork1221/Free-Proxies/refs/heads/main/proxy_files/socks4_proxies.txt`
  - `proxy_files/socks5_proxies.txt` → `https://raw.githubusercontent.com/Anonym0usWork1221/Free-Proxies/refs/heads/main/proxy_files/socks5_proxies.txt`
- Format: text — bare `ip:port`, protocol implied by file.

### elliottophellia/proxylist
- Repo: <https://github.com/elliottophellia/proxylist>
- File / raw URL: `results/pmix_checked.txt` → `https://raw.githubusercontent.com/elliottophellia/proxylist/refs/heads/master/results/pmix_checked.txt`
- Format: text — single mixed list ("pmix" = protocol mix); inspect actual line scheme during Phase 1 (may include prefixes).

### ProxyScraper/ProxyScraper
- Repo: <https://github.com/ProxyScraper/ProxyScraper>
- Files / raw URLs:
  - `http.txt` → `https://raw.githubusercontent.com/ProxyScraper/ProxyScraper/refs/heads/main/http.txt`
  - `socks4.txt` → `https://raw.githubusercontent.com/ProxyScraper/ProxyScraper/refs/heads/main/socks4.txt`
  - `socks5.txt` → `https://raw.githubusercontent.com/ProxyScraper/ProxyScraper/refs/heads/main/socks5.txt`
- Format: text — bare `ip:port`, protocol implied by file.
- Suspected duplicate of: TheSpeedX (same filenames, same scraping style).

### Argh94/Proxy-List
- Repo: <https://github.com/Argh94/Proxy-List>
- Files / raw URLs:
  - `HTTP.txt` → `https://raw.githubusercontent.com/Argh94/Proxy-List/refs/heads/main/HTTP.txt`
  - `HTTPS.txt` → `https://raw.githubusercontent.com/Argh94/Proxy-List/refs/heads/main/HTTPS.txt`
  - `SOCKS4.txt` → `https://raw.githubusercontent.com/Argh94/Proxy-List/refs/heads/main/SOCKS4.txt`
  - `SOCKS5.txt` → `https://raw.githubusercontent.com/Argh94/Proxy-List/refs/heads/main/SOCKS5.txt`
- Format: text — bare `ip:port`, protocol implied by file.
- Suspected duplicate of: TheSpeedX / ProxyScraper.

### zloi-user/hideip.me
- Repo: <https://github.com/zloi-user/hideip.me>
- Files / raw URLs:
  - `http.txt` → `https://github.com/zloi-user/hideip.me/raw/refs/heads/main/http.txt`
  - `https.txt` → `https://github.com/zloi-user/hideip.me/raw/refs/heads/main/https.txt`
  - `socks4.txt` → `https://github.com/zloi-user/hideip.me/raw/refs/heads/main/socks4.txt`
  - `socks5.txt` → `https://github.com/zloi-user/hideip.me/raw/refs/heads/main/socks5.txt`
- Format: text — bare `ip:port`, protocol implied by file.
- Notes: scraped from hideip.me; may carry country/anonymity metadata upstream that gets stripped in these txt dumps (verify in Phase 1).

### hookzof/socks5_list
- Repo: <https://github.com/hookzof/socks5_list>
- File / raw URL: `master/proxy.txt` → `https://raw.githubusercontent.com/hookzof/socks5_list/refs/heads/master/proxy.txt`
- Format: text — bare `ip:port`; socks5 only.
- Notes: actively checked socks5 list.

### ErcinDedeoglu/proxies
- Repo: <https://github.com/ErcinDedeoglu/proxies>
- Files / raw URLs:
  - `proxies/http.txt` → `https://raw.githubusercontent.com/ErcinDedeoglu/proxies/main/proxies/http.txt`
  - `proxies/https.txt` → `https://raw.githubusercontent.com/ErcinDedeoglu/proxies/main/proxies/https.txt`
  - `proxies/socks4.txt` → `https://raw.githubusercontent.com/ErcinDedeoglu/proxies/main/proxies/socks4.txt`
  - `proxies/socks5.txt` → `https://raw.githubusercontent.com/ErcinDedeoglu/proxies/main/proxies/socks5.txt`
- Format: text — bare `ip:port`, protocol implied by file.

### proxyscrape/free-proxy-list (mirror only — see also HTTP section)
- Repo: <https://github.com/proxyscrape/free-proxy-list>
- Path: `proxies/all/data.json`
- Fetch URL (jsdelivr CDN): `https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json`
- Format: json — same shape as proxifly's data.json.
- Role: **fallback only**, used when the live API endpoint below fails.

### sunny9577/proxy-scraper (GitHub Pages)
- Repo: <https://github.com/sunny9577/proxy-scraper>
- Fetch URL: `https://sunny9577.github.io/proxy-scraper/proxies.json`
- Format: json — schema to be confirmed during Phase 1 fetch test.

---

## Standalone HTTP endpoints

### ProxyScrape API
- URL: `https://api.proxyscrape.com/v4/free-proxy-list/get?request=get_proxies&proxy_format=protocolipport&format=json`
- Fallback: jsdelivr mirror above (`proxyscrape/free-proxy-list@main`)
- Format: json — array of strings in `protocolipport` form: `protocol:ip:port` (e.g. `http:1.2.3.4:8080`) → parse with regex on string items.

### GeoNode API
- URL template: `https://proxylist.geonode.com/api/proxy-list?page={page}&limit=500&sort_by=responseTime&sort_type=asc`
- Pagination: page-based, increment `{page}` from 1 until response returns zero proxies (500 max per page).
- Format: json — objects with ip, port, protocols (array), country, anonymityLevel, responseTime.

### free-proxy-list.net
- URL: `https://free-proxy-list.net/en/`
- Format: html — single-page table `.fpl-list` with columns: IP Address, Port, Code (country), Country (name), Anonymity, Google, Https (yes/no), Last Checked.
- Notes: provides country code + anonymity + https flag → skip those checks for rows where populated.

### Proxiware API
- URL template: `https://papi.proxiware.com/proxies?page={page}`
- Pagination: page-based, increment from 1 until empty response.
- Format: json — schema to confirm in Phase 1.

### proxydb.net
- URL template: `https://proxydb.net/?offset={offset}`
- Pagination: offset-based, 30 per request; increment `{offset}` by 30.
- Format: html — table with columns: IP, Port, Type (protocol), Country (flag img + abbr country code + city + ASN/ISP tooltip), Anonymity ("High Anonymous" etc.), ø Uptime (%), ø R-Time (e.g. `0.7s`), Gateway, Checked (last check timestamp).
- Notes: rich metadata (country, anonymity, uptime, response time); extract `<abbr>` for country code.

### Nodemaven Free Proxies
- URL: `https://freeproxies.nodemaven.com/proxies?per_page=1000000`
- Format: json — single large response, schema to confirm in Phase 1.

### RoundProxies API
- URL template: `https://roundproxies.com/api/get-free-proxies/?limit=500&page={page}&sort_by=lastChecked&sort_type=desc`
- Pagination: page-based, increment until a page returns zero proxies.
- Format: json — schema to confirm in Phase 1.

### Geonix API
- URL: `https://free.geonix.com/api/front/main/pagination/filtration`
- Method: POST — requires header `content-type: application/json`
- Body: `{"page":0,"size":1000000,"countries":[],"proxyProtocols":[],"proxyTypes":[]}`
- Format: json — schema to confirm in Phase 1 (single big response via size=1000000).

### advanced.name
- URL template: `https://advanced.name/freeproxy?page={page}`
- Pagination: page-based, keep incrementing while the table still contains proxies.
- Format: html — table extraction; columns to map in Phase 1.
