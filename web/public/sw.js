// Bumping the cache name drops entries written by older revisions. v3 served
// every /dashboard response cache-first and never revalidated, so one bad
// response (an HTML shell returned for a missing manifest.webmanifest) stayed
// pinned for the lifetime of the browser profile.
const CACHE = "tproxy-dashboard-v4";
const PRECACHE = ["/dashboard/", "/dashboard/manifest.webmanifest"];
// Build output under assets/ is content-hashed, so it is safe to serve from the
// cache without revalidating. Everything else must hit the network first.
const IMMUTABLE_PREFIX = "/dashboard/assets/";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      // A failed precache must not block activation; the fetch handler falls
      // back to the network anyway.
      .catch(() => undefined)
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") return;
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin || !url.pathname.startsWith("/dashboard/")) return;
  event.respondWith(
    url.pathname.startsWith(IMMUTABLE_PREFIX) ? cacheFirst(event.request) : networkFirst(event.request),
  );
});

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  await store(request, response);
  return response;
}

async function networkFirst(request) {
  try {
    const response = await fetch(request);
    await store(request, response);
    return response;
  } catch (error) {
    const cached = await caches.match(request);
    if (cached) return cached;
    throw error;
  }
}

async function store(request, response) {
  // Opaque and error responses carry a body the page cannot parse. Caching one
  // is what made the manifest unreadable on every subsequent load.
  if (!response.ok || response.type !== "basic") return;
  const cache = await caches.open(CACHE);
  await cache.put(request, response.clone());
}
