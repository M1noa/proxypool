# proxypool

![proxies](https://img.shields.io/badge/total%20proxies-2385-blue) ![avg response](https://img.shields.io/badge/avg%20response-3650ms-orange) ![last run](https://img.shields.io/github/last-commit/M1noa/proxypool) ![successrate](https://img.shields.io/badge/success%20rate-3%25-red)

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
| hosting | 882 |
| isp | 801 |
| business | 679 |
| unknown | 13 |
| education_research | 9 |
| government_admin | 1 |
<!-- types:end -->

<!-- countries:start -->
| country | proxies |
|---|---|
| CN | 482 |
| ID | 326 |
| US | 128 |
| PH | 116 |
| other | 2170 |
<!-- countries:end -->

<!-- anon:start -->
| anonymity | proxies |
|---|---|
| unknown | 984 |
| anonymous | 509 |
| transparent | 492 |
| elite | 400 |
<!-- anon:end -->

<!-- proto:start -->
| type | proxies |
|---|---|
| http | 2020 |
| socks4 | 1177 |
| socks5 | 1056 |
| https | 828 |
<!-- proto:end -->

<!-- ports:start -->
| port | proxies |
|---|---|
| 8080 | 403 |
| 999 | 218 |
| 3128 | 169 |
| 80 | 153 |
| 8081 | 72 |
<!-- ports:end -->

<!-- sources:start -->
| source | fetched | alive | success |
|---|---|---|---|
| proxifly | 2338 | 91 | 4% |
| geonode | 3174 | 220 | 7% |
| nodemaven | 1975 | 564 | 29% |
| roundproxies | 3119 | 214 | 7% |
| proxiware | 4697 | 386 | 8% |
| sunny9577 | 911 | 172 | 19% |
| proxyscrape | 1821 | 290 | 16% |
| speedx-http | 2215 | 294 | 13% |
| speedx-socks4 | 1909 | 201 | 11% |
| speedx-socks5 | 1453 | 170 | 12% |
| anonymouse-http | 3059 | 300 | 10% |
| anonymouse-socks4 | 716 | 31 | 4% |
| anonymouse-socks5 | 418 | 9 | 2% |
| argh94-http | 2798 | 290 | 10% |
| argh94-socks4 | 283 | 12 | 4% |
| argh94-socks5 | 171 | 4 | 2% |
| hideip-http | 206 | 47 | 23% |
| hideip-https | 1227 | 29 | 2% |
| hideip-socks4 | 401 | 58 | 14% |
| hideip-socks5 | 265 | 25 | 9% |
| ercin-http | 62375 | 2131 | 3% |
| ercin-https | 2976 | 88 | 3% |
| ercin-socks4 | 21029 | 1116 | 5% |
| ercin-socks5 | 22486 | 1181 | 5% |
| iplocate-all | 2844 | 501 | 18% |
| elliottophellia-pmix | 265 | 65 | 25% |
| freeproxylist-net | 299 | 120 | 40% |
| proxydb | 706 | 151 | 21% |
| advanced-name | 515 | 138 | 27% |
| ditatompel | 100 | 28 | 28% |
<!-- sources:end -->
