# proxypool

![proxies](https://img.shields.io/badge/total%20proxies-2209-blue) ![avg response](https://img.shields.io/badge/avg%20response-3630ms-orange) ![last check](https://img.shields.io/badge/last%20check-2026--08--26-green)

hourly refreshed proxy lists. fetched from public sources, merged, checked live, dead ones dropped.

| file | what |
|---|---|
| [http.txt](output/http.txt) / [https.txt](output/https.txt) / [socks4.txt](output/socks4.txt) / [socks5.txt](output/socks5.txt) / [all.txt](output/all.txt) | `ip:port` per line, sorted by response time |
| [proxies.json](output/proxies.json) | full details: protocols, country, anonymity, response time, sources |

sources configurable in [`sources.jsonc`](sources.jsonc). country + asn data from [db-ip lite](https://db-ip.com); `ip_type` (hosting/residential) from [ipverse/as-metadata](https://github.com/ipverse/as-metadata) (cc0).

<!-- types:start -->
| type | proxies |
|---|---|
| hosting | 906 |
| isp | 824 |
| business | 443 |
| unknown | 24 |
| education_research | 10 |
| government_admin | 2 |
<!-- types:end -->

<!-- countries:start -->
| country | proxies |
|---|---|
| ID | 406 |
| CN | 257 |
| US | 149 |
| PH | 88 |
| other | 1923 |
<!-- countries:end -->
