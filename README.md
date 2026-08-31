# [proxy pool](https://proxies.minoa.cat)

![total](https://img.shields.io/badge/3420-brightgreen) <!--badge:total--> ![sources](https://img.shields.io/badge/137-blue) <!--badge:sources--> ![updated](https://img.shields.io/badge/2026--08--29%2021%3A49%20UTC-green) <!--badge:updated--> ![fetch](https://github.com/M1noa/proxypool/actions/workflows/fetch.yml/badge.svg)

hourly refreshed proxy lists. fetched from public sources, merged, checked live, dead ones dropped.

outputs live on the auto-reset [`output`](https://github.com/M1noa/proxypool/tree/output) branch (always 1 commit, easy to clone):

| file | what |
|---|---|
| [proxies.json](https://raw.githubusercontent.com/M1noa/proxypool/output/proxies.json) | all live proxies, sorted by response time: protocols, country, anonymity, sources |
| [history.duckdb](https://raw.githubusercontent.com/M1noa/proxypool/output/history.duckdb) | per-proxy reliability/quality history (duckdb) |

source columns (only fields a source actually has are kept):

`ip` `port` `protocols` `https` `anonymity` `country` `isp` `asn` `source` `sources`

a few of the bigger sources:

| source | link |
|---|---|
| TheSpeedX/PROXY-List (http) | [github.com/TheSpeedX/PROXY-List](https://github.com/TheSpeedX/PROXY-List) |
| jetkai/proxy-list | [github.com/jetkai/proxy-list](https://github.com/jetkai/proxy-list) |
| monosans/proxy-list | [github.com/monosans/proxy-list](https://github.com/monosans/proxy-list) |

137 total in [sources.jsonc](sources.jsonc).
