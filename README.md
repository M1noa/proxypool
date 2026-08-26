# proxypool

Hourly-refreshed proxy lists: 29 public sources, merged, deduped, live-checked.

| file | contents |
|---|---|
| [proxies.json](output/proxies.json) | alive proxies w/ full metadata (protocol, country, anonymity, RT), fastest first |
| [http.txt](output/http.txt) / [https.txt](output/https.txt) / [socks4.txt](output/socks4.txt) / [socks5.txt](output/socks5.txt) | `ip:port` per line |

```bash
pip install -r requirements.txt
python3 fetch_proxies.py
```

Sources are defined entirely in [`sources.jsonc`](sources.jsonc) — adding one is config, not code. Country data from [DB-IP Lite](https://db-ip.com).
