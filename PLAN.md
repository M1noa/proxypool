# PLAN — proxies.minoa.cat

edge-cached proxy list api + url generator for [proxypool](https://github.com/M1noa/proxypool).

## architecture

single cloudflare worker with static assets (`public/`). no frameworks, no build step
beyond `wrangler deploy`. typescript, strict, no `any`.

```
GET /            static url-generator ui (works without js)
GET /list        the api. filters -> sort -> limit -> render
GET /docs.json   machine-readable api spec (static asset)
/style.css /script.js /noise.png   static assets
```

## data

upstream: `https://raw.githubusercontent.com/M1noa/proxypool/output/proxies.json`
(orphan `output` branch, force-pushed hourly by github actions).

record shape (json array):

```
ip, ip_version (ipv4|ipv6), port, protocols[http|https|socks4|socks5],
country (iso2|""), anonymity (elite|anonymous|transparent|""), https (bool),
sources[], source_meta, last_checked, response_time_ms, response_time_raw_ms,
asn, as_org, ip_type (hosting|business|isp|...), reliability (0-1),
quality (0-1), checks_total, checks_ok, first_seen, last_seen
```

## caching

1. upstream fetch with `cf: { cacheTtl: 30, cacheEverything: true }` —
   github is hit at most ~once per 30s per colo.
2. rendered responses cached in `caches.default` keyed by canonical url
   (params sorted, deduped, lowercased, defaults stripped). ttl 30s.
   writes via `ctx.waitUntil`.
3. client-facing `Cache-Control: public, max-age=30`.

debug headers: `X-Cache: HIT|MISS`, `X-Proxy-Count`, `X-Data-Age`.

## api: GET /list

all params optional. multi-value params repeat or comma-separate.
unknown params and invalid values are 400s, rendered in the requested format.

| param | values | default |
|---|---|---|
| type | http, https, socks4, socks5 (multi) | all |
| anonymity | elite, anonymous, transparent, unknown (multi) | all |
| country | iso2 codes (multi) | all |
| port | ints (multi); port_min, port_max | all |
| asn | ints (multi) | all |
| as_org | substring, case-insensitive | all |
| source | source names (multi) | all |
| ip_version | ipv4, ipv6 | all |
| ip_type | hosting, business, isp, ... (multi) | all |
| https | true, false | both |
| min_reliability | 0..1 | 0 |
| min_quality | 0..1 | 0 |
| response_min, response_max | ms ints | unbounded |
| first_seen_after, last_seen_after | iso 8601 | unbounded |
| sort | response, reliability, quality, first_seen, last_seen, port, country, asn, random | response |
| order | asc, desc | per-sort (response=asc, rest=desc) |
| limit | int, 0=all | 0 |
| format | txt, json, csv, jsonl | txt |

### txt protocol rule

- exactly one type selected -> bare `ip:port` lines
- zero or 2+ types -> `protocol://ip:port`, one line per matched protocol per proxy

### formats

- `txt`  — lines as above, `text/plain`
- `json` — filtered array of full records, `application/json`
- `jsonl` — one record per line, `application/x-ndjson`
- `csv`  — header + all fields; arrays joined with `;`, rfc4180 quoting, `text/csv`

### errors, in-format

400 unknown param / invalid value (message lists valid values), 502 upstream failure,
500 anything else. body rendered in the requested format:
txt -> `error <code>: message` line, json/jsonl -> `{"error":{"code","message"}}`,
csv -> `error,message` header + row.

## frontend

copies crypto.minoa.cat design: black bg, animated `noise.png`, Rubik 80s Fade
"ProxyPool" header, Sora body, Geist Mono for the url preview, same card/button/
notification/footer styles, random pink/white/pastel theme per load.
unused css (wallets, modal, qr) stripped.

the page is a url generator: every filter as form controls, live-built url,
copy + open buttons. without js the native GET form still submits to /list,
and the base url + docs link are shown.

## files

```
wrangler.jsonc   config (jsonc, compat date, observability, static assets)
package.json     wrangler devDep, scripts
tsconfig.json    strict, workers types
src/index.ts     router + cache + error boundary
src/params.ts    query parse/validate -> normalized spec
src/filter.ts    filter/sort/limit
src/render.ts    txt/json/jsonl/csv + in-format errors
src/data.ts      upstream fetch + dataset cache
public/          index.html style.css script.js noise.png docs.json
```

## workers best-practice rules applied

from cloudflare's workers-best-practices skill (fetched 2026-08-30):

- wrangler.jsonc, fresh compatibility_date
- `wrangler types` generated Env (no hand-written Env)
- no secrets in config/source (none needed at all)
- no `await response.text()` on unbounded bodies (upstream json is bounded, ~mb)
- no module-level request state (module-level *data* cache only, immutable once set)
- every promise awaited / returned / `ctx.waitUntil`ed
- `satisfies ExportedHandler<Env>`, no `any`, no double-casts
- explicit try/catch error boundary, structured json logging, observability on
- no `Math.random()` for anything security-relevant (only used for sort=random)

## deploy

`wrangler deploy`, custom domain `proxies.minoa.cat` via
`routes = [{ pattern = "proxies.minoa.cat", custom_domain = true }]`.
