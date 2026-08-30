# AGENT.md

Proxy aggregator. Fetches proxies from `sources.jsonc`, checks them, writes deduped `output/*.txt`/`proxies.json` and refreshes README tables/badges on each run. ~67 sources; ~30 were ported from CharlesPikachu/freeproxy.

## Setup
- `python3 fetch_proxies.py` runs one fetch+check cycle.
- Hourly cron in `.github/workflows` commits refreshed outputs + README.
- Utility scripts live in `tools/` (`test_source.py`, `probe_endpoints.py`, `check_github_activity.py`, `check_repos.py`). History state is `output/history.db` (gitignored).

## Test one source (no check)
- `python3 tools/test_source.py <name> [--max-pages N] [--show N] [--list]` — fetch a single source without running the checker, print raw record count + samples.
- `python3 tools/probe_endpoints.py` — fetch every source (no checker), report ok/empty/error per source.

## Adding a new source
Before adding any source to `sources.jsonc`:

1. **Verify it has original proxies.** Sample the API/HTML head and tail; confirm it returns live `ip:port` pairs, not an empty feed or just metadata.
2. **Confirm it is not an exact clone of an existing source.** Compare the head/tail and a sample of `ip:port` rows against every other source already in `sources.jsonc`. If it duplicates another source (same upstream, different mirror), do not add it.
3. **Configure it fully.** Set `format`/`extract` and `pagination` completely — `type`, `start`, `step`, `stop`, `limit`, `max_pages`, and `total_path` (for page-count from server `total`) — so the full feed is walked, not just page 1.
4. **Anti-bot / complex feeds.** Set `antibot: true` to send realistic browser headers. For sites needing token discovery or JS eval, use `prefetch` steps (`regex`/`json_path`/`as_url`/`store`) or a named `flow` in `lib/flows.py` (`proxyshare`, `proxynova`, `spysone`). HTML tables can embed JSON via `extract.embedded_json` (`selector` + `regex`); constant field values via `{"const": ...}`; obfuscated cells via `decode: base64_reverse`. `proxynova`/`spysone` need `pip install quickjs`.
4. **No bogus IPs.** Bogus addresses (loopback, private/RFC1918, link-local, multicast, reserved, 0.0.0.0/8, 255.255.255.255, `localhost`) and records missing a port are dropped automatically in `lib/parse._norm_record`. Each record gets an `ip_version` (`ipv4`/`ipv6`/`domain`).

## Gotchas
- `check_all` drops dead proxies — post-check `records` contain only alive proxies.
- The sources table's "top countries" column shows the top 2 countries (comma-separated).
