# proxypool

![proxies](https://img.shields.io/badge/total%20proxies-2441-blue) ![avg response](https://img.shields.io/badge/avg%20response-3581ms-orange) ![last check](https://img.shields.io/badge/last%20check-2026--08--27-green)

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
| isp | 943 |
| hosting | 903 |
| business | 570 |
| unknown | 13 |
| education_research | 11 |
| government_admin | 1 |
<!-- types:end -->

<!-- countries:start -->
| country | proxies |
|---|---|
| ID | 395 |
| CN | 344 |
| US | 173 |
| PH | 98 |
| other | 2185 |
<!-- countries:end -->
