# AGENT.md

Proxy aggregator. Fetches proxies from `sources.jsonc`, checks them, writes deduped `output/*.txt`/`proxies.json` and refreshes README tables/badges on each run.

## Setup
- `python3 fetch_proxies.py` runs one fetch+check cycle.
- Hourly cron in `.github/workflows` commits refreshed outputs + README.

## Adding a new source
Before adding any source to `sources.jsonc`:

1. **Verify it has original proxies.** Sample the API/HTML head and tail; confirm it returns live `ip:port` pairs, not an empty feed or just metadata.
2. **Confirm it is not an exact clone of an existing source.** Compare the head/tail and a sample of `ip:port` rows against every other source already in `sources.jsonc`. If it duplicates another source (same upstream, different mirror), do not add it.
3. **Configure it fully.** Set `format`/`extract` and `pagination` completely — `type`, `start`, `step`, `stop`, `limit`, `max_pages`, and `total_path` (for page-count from server `total`) — so the full feed is walked, not just page 1.
4. **No bogus IPs.** Bogus addresses (loopback, private/RFC1918, link-local, multicast, reserved, 0.0.0.0/8, 255.255.255.255, `localhost`) and records missing a port are dropped automatically in `lib/parse._norm_record`. Each record gets an `ip_version` (`ipv4`/`ipv6`/`domain`).

## Gotchas
- The successrate badge counts only source IPs (proxies seen in a source this run); recycled proxies carried from the previous run are excluded so the metric reflects live sourcing.
- `check_all` drops dead proxies — post-check `records` contain only alive proxies.
