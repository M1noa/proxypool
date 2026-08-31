// entrypoint: route, cache, error boundary.
// static assets (/, /docs.json, /style.css...) are served by worker assets;
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
      "Cache-Control": `public, max-age=${env.CACHE_TTL_SECONDS}`,
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
        return new Response("not found. see /docs\n", {
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
} satisfies ExportedHandler<Env>;
