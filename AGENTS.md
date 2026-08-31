# AGENTS.md

edge api + tiny static site serving filtered proxy lists from the proxypool
`output` branch. cloudflare worker + static assets. lives on orphan branch
`pages-cache`, deploys to proxies.minoa.cat.

## layout

- `src/index.ts` — router, response cache, error boundary. entrypoint.
- `src/params.ts` — query string -> validated, normalized filter spec.
  every rule about valid values lives here.
- `src/data.ts` — fetches proxies.json from github raw, 30s edge cache.
- `src/filter.ts` — applies spec to records, sorts, limits.
- `src/render.ts` — txt / json / jsonl / csv renderers + in-format errors.
- `public/` — static assets (index.html, style.css, script.js, noise.png,
  docs.json). served by worker static assets, no handler code.
- `PLAN.md` — architecture decisions. `TODO.md` — live task list.

## commands

- `npm install` — deps (wrangler + types only)
- `npm run dev` — wrangler dev
- `npm run deploy` — wrangler deploy
- `npm run check` — tsc --noEmit (must pass before committing)

## rules (from cloudflare workers-best-practices, fetched 2026-08-30)

- wrangler.jsonc only, never toml. keep compatibility_date fresh.
- never hand-write the Env interface — run `wrangler types` after config changes.
- no secrets in config or source. this project needs none.
- no module-level *request* state. module-level cache of immutable dataset is ok
  (see src/data.ts).
- every promise: awaited, returned, or passed to `ctx.waitUntil`. never
  destructure ctx.
- `satisfies ExportedHandler<Env>` on the default export. no `any`,
  no `as unknown as X`.
- explicit try/catch with structured errors — no `passThroughOnException`.
- structured json logging (`console.log(JSON.stringify(...))`).
- don't buffer unbounded bodies. the upstream json is bounded (~few mb) so
  `response.json()` is fine there and only there.

## api contract

everything user-facing is documented in `public/docs.json`. if you change
params, defaults, sorts, or formats, update docs.json and PLAN.md in the
same commit. errors must render in the requested format (see render.ts).

## style

- typescript strict, no deps beyond wrangler/types.
- plain web apis (URL, URLSearchParams, caches, crypto).
- comments: short, lowercase, only when non-obvious.
- frontend must work with javascript disabled (native GET form to /list).
