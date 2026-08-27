# proxypool

![proxies](https://img.shields.io/badge/total%20proxies-2151-blue) ![avg response](https://img.shields.io/badge/avg%20response-3640ms-orange) ![last run](https://img.shields.io/github/last-commit/M1noa/proxypool)

---

hourly refreshed proxy lists. fetched from public sources, merged, checked live, dead ones dropped.

| file | what |
|---|---|
| [http.txt](output/http.txt) / [https.txt](output/https.txt) / [socks4.txt](output/socks4.txt) / [socks5.txt](output/socks5.txt) / [all.txt](output/all.txt) | `ip:port` per line, sorted by response time |
| [proxies.json](output/proxies.json) | full details: protocols, country, anonymity, response time, sources |

sources configurable in [`sources.jsonc`](sources.jsonc). country + asn data from [db-ip lite](https://db-ip.com); `ip_type` (hosting/residential) from [ipverse/as-metadata](https://github.com/ipverse/as-metadata) (cc0).

---

<!-- types:start -->
| type | proxies |
|---|---|
| hosting | 871 |
| isp | 827 |
| business | 416 |
| unknown | 25 |
| education_research | 10 |
| government_admin | 2 |
<!-- types:end -->

<!-- countries:start -->
| country | proxies |
|---|---|
| ID | 373 |
| CN | 229 |
| US | 193 |
| PH | 82 |
| other | 1903 |
<!-- countries:end -->

<!-- sources:start -->
<!-- sources:end -->
