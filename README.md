# proxypool

![proxies](https://img.shields.io/badge/total%20proxies-2558-blue) ![avg response](https://img.shields.io/badge/avg%20response-3793ms-orange) ![last check](https://img.shields.io/badge/last%20check-2026--08--26-green)

hourly refreshed proxy lists. fetched from public sources, merged, checked live, dead ones dropped.

| file | what |
|---|---|
| [http.txt](output/http.txt) / [https.txt](output/https.txt) / [socks4.txt](output/socks4.txt) / [socks5.txt](output/socks5.txt) / [all.txt](output/all.txt) | `ip:port` per line, sorted by response time |
| [proxies.json](output/proxies.json) | full details: protocols, country, anonymity, response time, sources |

sources configurable in [`sources.jsonc`](sources.jsonc). country + asn data from [db-ip lite](https://db-ip.com); `ip_type` (hosting/residential) from [ipverse/as-metadata](https://github.com/ipverse/as-metadata) (cc0).

<!-- types:start -->
| type | proxies |
|---|---|
| hosting | 912 |
| isp | 910 |
| business | 706 |
| unknown | 14 |
| education_research | 12 |
| government_admin | 4 |
<!-- types:end -->

<!-- countries:start -->
| country | proxies |
|---|---|
| CN | 497 |
| ID | 437 |
| US | 145 |
| PH | 102 |
| other | 2337 |
<!-- countries:end -->
