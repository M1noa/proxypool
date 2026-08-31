# proxies.minoa.cat

> THIS PROJECT WAS MADE PARTIALLY USING AGENTIC AI CODING TOOLS

fast filtered proxy lists, served from the edge. data comes from the
[proxypool](https://github.com/M1noa/proxypool) `output` branch, refreshed
hourly, cached at cloudflare for 30s.

## use it

```
https://proxies.minoa.cat/list?type=socks4&type=socks5&format=txt&sort=response&limit=0
```

- `format=txt|json|jsonl|csv`
- filter by type, anonymity, country, port, asn, source, ip version,
  ip type, response time, reliability, quality, first/last seen
- sort by response, reliability, quality, first_seen, last_seen, port,
  country, asn, random
- full spec: <https://proxies.minoa.cat/docs.json>
- build a url interactively: <https://proxies.minoa.cat/>

## develop

```
npm install
npm run dev      # local
npm run check    # typecheck
npm run deploy   # ship it
```

see PLAN.md for architecture and AGENTS.md for contributor rules.
