/*
 * agentsfs hub — service worker.
 *
 * Deliberately conservative: it makes the hub installable and gives the static
 * shell an offline fallback, but it NEVER intercepts the live agent, its APIs,
 * the LLM proxy, previews, or git — those must always hit the network untouched
 * (SSE streams and POSTs in particular). It only handles same-origin GETs:
 *   - /_assets/*  → stale-while-revalidate (fast loads, refreshed in background)
 *   - navigations → network-first, falling back to cache, then an offline card
 */

const CACHE = "afs-hub-v1";

// Paths the worker must leave entirely alone (return without respondWith so the
// browser does its normal fetch): the proxied agent + its APIs, the model proxy,
// previews, and auth round-trips.
const PASSTHROUGH = /^\/(agent|api|v1|preview|healthz)(\/|$)/;

self.addEventListener("install", (event) => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)));
      await self.clients.claim();
    })(),
  );
});

function offlineCard() {
  return new Response(
    "<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'>" +
      "<title>Offline</title>" +
      "<div style=\"font:16px/1.5 -apple-system,system-ui,sans-serif;max-width:32rem;margin:20vh auto;padding:0 1.5rem;text-align:center;color:#243\">" +
      "<h1 style=\"font-size:1.3rem\">You're offline</h1>" +
      "<p>agentsfs hub can't reach the network right now. Your knowledge is safe — reconnect and reload.</p>" +
      "<p><button onclick=\"location.reload()\" style=\"padding:.6rem 1.1rem;border:0;border-radius:8px;background:#18c987;color:#04150f;font-weight:600\">Retry</button></p></div>",
    { headers: { "Content-Type": "text/html; charset=utf-8" }, status: 200 },
  );
}

async function staleWhileRevalidate(req) {
  const cache = await caches.open(CACHE);
  const cached = await cache.match(req);
  const network = fetch(req)
    .then(async (res) => {
      if (res && res.ok && res.type === "basic") {
        await cache.put(req, res.clone());
        // Keep exactly one entry per asset path: drop older ?v= revisions of the
        // same file so the cache doesn't accumulate a full copy every deploy.
        const path = new URL(req.url).pathname;
        for (const k of await cache.keys()) {
          if (k.url !== req.url && new URL(k.url).pathname === path) {
            await cache.delete(k);
          }
        }
      }
      return res;
    })
    .catch(() => cached);
  return cached || network;
}

// Page navigations are always fetched fresh and NEVER written to the cache: they
// can be private, authenticated HTML (a user's dashboard, a private note), and
// Cache Storage is origin-scoped, not per-user — persisting them would leak one
// account's pages to another local user on a shared browser, or serve stale
// content after logout. The only offline fallback is the generic offline card.
async function networkFirst(req) {
  try {
    return await fetch(req);
  } catch (e) {
    return offlineCard();
  }
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  let url;
  try {
    url = new URL(req.url);
  } catch {
    return;
  }
  // Only ever touch our own origin, GET only.
  if (req.method !== "GET" || url.origin !== self.location.origin) return;
  // The live agent, APIs, model proxy, previews, health: hands off, always.
  if (PASSTHROUGH.test(url.pathname)) return;
  // Static shell assets.
  if (url.pathname.startsWith("/_assets/")) {
    event.respondWith(staleWhileRevalidate(req));
    return;
  }
  // Full-page navigations (the app shell).
  if (req.mode === "navigate") {
    event.respondWith(networkFirst(req));
    return;
  }
  // Anything else: let the network handle it.
});
