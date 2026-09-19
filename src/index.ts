// entrypoint: route, cache, error boundary.
// static assets (/, /docs.json, /style.css, ...) are served by worker assets;
// only /list is handled here.

import {
  ApiError,
  canonicalQuery,
  matchShortcut,
  parseParams,
  specFromShortcut,
  type Format,
  type Shortcut,
} from "./params";
import { getDataset } from "./data";
import { applyFilters } from "./filter";
import { CONTENT_TYPES, render, renderError } from "./render";
import { getUADataset } from "./ua-data";
import { applyUAFilters } from "./ua-filter";
import {
  canonicalUAQuery,
  matchUAShortcut,
  parseUAParams,
  specFromUAShortcut,
  type UAFormat,
  type UAFilterSpec,
  type UAShortcut,
} from "./ua-params";
import { renderUA, renderUAError } from "./ua-render";
import { watchdog } from "./watchdog";

function errorResponse(format: Format, status: number, code: string, message: string): Response {
  return new Response(renderError(format, status, code, message), {
    status,
    headers: {
      "Content-Type": CONTENT_TYPES[format],
      "Cache-Control": "no-store",
    },
  });
}

// best-effort format detection for errors thrown before params are parsed.
// a shortcut path's extension wins over ?format=
function guessFormat(url: URL): Format {
  const shortcut = matchShortcut(url.pathname);
  if (shortcut) return shortcut.format;
  const f = (url.searchParams.get("format") || "txt").toLowerCase();
  return f === "json" || f === "jsonl" || f === "csv" ? f : "txt";
}

function guessUAFormat(url: URL): UAFormat {
  const shortcut = matchUAShortcut(url.pathname);
  if (shortcut) return shortcut.format;
  const f = (url.searchParams.get("format") || "txt").toLowerCase();
  return f === "json" || f === "jsonl" || f === "csv" ? f : "txt";
}

function uaErrorResponse(format: UAFormat, status: number, code: string, message: string): Response {
  return new Response(renderUAError(format, status, code, message), {
    status,
    headers: {
      "Content-Type": CONTENT_TYPES[format],
      "Cache-Control": "no-store",
    },
  });
}

// small json summary for the badges on the ui -- total count, average
// response time, most recent last_checked. cached same as /list.
async function handleStats(env: Env, ctx: ExecutionContext, url: URL): Promise<Response> {
  const cacheKey = new Request(`${url.origin}/stats`, { method: "GET" });
  const cache = caches.default;
  const hit = await cache.match(cacheKey);
  if (hit) return new Response(hit.body, hit);

  const dataset = await getDataset(env);
  const total = dataset.records.length;
  const avgResponseMs = total
    ? Math.round(dataset.records.reduce((sum, r) => sum + r.response_time_ms, 0) / total)
    : 0;
  const lastCheck = dataset.records.reduce(
    (latest, r) => (r.last_checked > latest ? r.last_checked : latest),
    "",
  );

  const res = new Response(JSON.stringify({ total, avg_response_ms: avgResponseMs, last_check: lastCheck }), {
    status: 200,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": `public, max-age=${env.CACHE_TTL_SECONDS}, stale-while-revalidate=60`,
    },
  });
  ctx.waitUntil(cache.put(cacheKey, res.clone()));
  return res;
}

// small json summary for the badges on the agents ui -- total agents, top
// browser/os, newest generation time. cached same as /uas.
async function handleUAStats(env: Env, ctx: ExecutionContext, url: URL): Promise<Response> {
  const cacheKey = new Request(`${url.origin}/ua-stats`, { method: "GET" });
  const cache = caches.default;
  const hit = await cache.match(cacheKey);
  if (hit) return new Response(hit.body, hit);

  const dataset = await getUADataset(env);
  const byBrowser = new Map<string, number>();
  const byOS = new Map<string, number>();
  let generatedAt = "";
  for (const r of dataset.records) {
    byBrowser.set(r.browser, (byBrowser.get(r.browser) ?? 0) + 1);
    byOS.set(r.os, (byOS.get(r.os) ?? 0) + 1);
    if (r.generated_at > generatedAt) generatedAt = r.generated_at;
  }
  const top = (m: Map<string, number>): string =>
    [...m.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] ?? "";

  const res = new Response(
    JSON.stringify({
      total: dataset.records.length,
      top_browser: top(byBrowser),
      top_os: top(byOS),
      generated_at: generatedAt,
    }),
    {
      status: 200,
      headers: {
        "Content-Type": "application/json; charset=utf-8",
        "Cache-Control": `public, max-age=${env.CACHE_TTL_SECONDS}, stale-while-revalidate=60`,
      },
    },
  );
  ctx.waitUntil(cache.put(cacheKey, res.clone()));
  return res;
}

async function handleUAList(
  env: Env,
  ctx: ExecutionContext,
  url: URL,
  shortcut: UAShortcut | null,
): Promise<Response> {
  // bare /uas: full upstream useragents.json, untouched
  const bare = url.pathname === "/uas" && url.search === "";
  const spec: UAFilterSpec = shortcut ? specFromUAShortcut(url, shortcut) : parseUAParams(url);

  const cacheable = spec.sort !== "random";
  const cacheKey = new Request(
    bare ? `${url.origin}/uas` : `${url.origin}/uas?${canonicalUAQuery(spec)}`,
    { method: "GET" },
  );
  const cache = caches.default;

  if (cacheable) {
    const hit = await cache.match(cacheKey);
    if (hit) {
      const res = new Response(hit.body, hit);
      res.headers.set("X-Cache", "HIT");
      return res;
    }
  }

  const dataset = await getUADataset(env);
  const records = applyUAFilters(dataset.records, spec);
  const body = bare ? dataset.raw : renderUA(records, spec.format);
  const count = records.length;

  const dataAgeSec = Math.max(0, Math.round((Date.now() - dataset.fetchedAt) / 1000));
  const res = new Response(body, {
    status: 200,
    headers: {
      "Content-Type": bare ? CONTENT_TYPES.json : CONTENT_TYPES[spec.format],
      "Cache-Control": `public, max-age=${env.CACHE_TTL_SECONDS}, stale-while-revalidate=60`,
      "X-Cache": "MISS",
      "X-UA-Count": String(count),
      "X-Data-Age": String(dataAgeSec),
    },
  });

  if (cacheable) {
    ctx.waitUntil(cache.put(cacheKey, res.clone()));
  }
  return res;
}

async function handleList(
  env: Env,
  ctx: ExecutionContext,
  url: URL,
  shortcut: Shortcut | null,
): Promise<Response> {
  // bare /list: full upstream proxies.json, untouched
  const bare = url.pathname === "/list" && url.search === "";
  const spec = shortcut ? specFromShortcut(url, shortcut) : parseParams(url);

  // random order is never cached — every call must reshuffle
  const cacheable = spec.sort !== "random";
  const cacheKey = new Request(
    bare ? `${url.origin}/list` : `${url.origin}/list?${canonicalQuery(spec)}`,
    { method: "GET" },
  );
  const cache = caches.default;

  if (cacheable) {
    const hit = await cache.match(cacheKey);
    if (hit) {
      const res = new Response(hit.body, hit);
      res.headers.set("X-Cache", "HIT");
      return res;
    }
  }

  const dataset = await getDataset(env);
  const records = applyFilters(dataset.records, spec);
  const body = bare ? dataset.raw : render(records, spec);
  const count = records.length;

  const dataAgeSec = Math.max(0, Math.round((Date.now() - dataset.fetchedAt) / 1000));
  const res = new Response(body, {
    status: 200,
    headers: {
      "Content-Type": bare ? CONTENT_TYPES.json : CONTENT_TYPES[spec.format],
      "Cache-Control": `public, max-age=${env.CACHE_TTL_SECONDS}, stale-while-revalidate=60`,
      "X-Cache": "MISS",
      "X-Proxy-Count": String(count),
      "X-Data-Age": String(dataAgeSec),
    },
  });

  if (cacheable) {
    ctx.waitUntil(cache.put(cacheKey, res.clone()));
  }
  return res;
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    try {
      if (url.pathname === "/stats") {
        return await handleStats(env, ctx, url);
      }
      if (url.pathname === "/ua-stats") {
        return await handleUAStats(env, ctx, url);
      }
      const uaShortcut = matchUAShortcut(url.pathname);
      if (url.pathname === "/uas" || uaShortcut) {
        if (request.method !== "GET" && request.method !== "HEAD") {
          return uaErrorResponse(guessUAFormat(url), 405, "method_not_allowed", "only GET is supported");
        }
        try {
          return await handleUAList(env, ctx, url, uaShortcut);
        } catch (e) {
          if (e instanceof ApiError) {
            console.log(JSON.stringify({ message: "ua api error", status: e.status, code: e.code, detail: e.message }));
            return uaErrorResponse(guessUAFormat(url), e.status, e.code, e.message);
          }
          throw e;
        }
      }
      const shortcut = matchShortcut(url.pathname);
      if (url.pathname !== "/list" && !shortcut) {
        // assets already had their chance at this path.
        // browsers get the styled page, scripts get one plain line.
        if (request.headers.get("Accept")?.includes("text/html")) {
          const page = await env.ASSETS.fetch(new URL("/404.html", url.origin));
          return new Response(page.body, {
            status: 404,
            headers: { "Content-Type": "text/html; charset=utf-8" },
          });
        }
        return new Response("not found. see /docs.json\n", {
          status: 404,
          headers: { "Content-Type": "text/plain; charset=utf-8" },
        });
      }
      if (request.method !== "GET" && request.method !== "HEAD") {
        return errorResponse(guessFormat(url), 405, "method_not_allowed", "only GET is supported");
      }
      return await handleList(env, ctx, url, shortcut);
    } catch (e) {
      const format = guessFormat(url);
      if (e instanceof ApiError) {
        console.log(JSON.stringify({ message: "api error", status: e.status, code: e.code, detail: e.message }));
        return errorResponse(format, e.status, e.code, e.message);
      }
      console.error(JSON.stringify({ message: "unhandled error", error: e instanceof Error ? e.message : String(e), path: url.pathname }));
      return errorResponse(format, 500, "internal_error", "something broke on our end");
    }
  },

  // cron trigger, unrelated to serving. see watchdog.ts -- it nudges the fetch
  // workflow when github's scheduler has gone quiet for too long.
  async scheduled(_controller: ScheduledController, env: Env, ctx: ExecutionContext): Promise<void> {
    ctx.waitUntil(watchdog(env));
  },
} satisfies ExportedHandler<Env>;
