/* global caches, fetch, self, URL */

const CACHE_PREFIX = "memento-shell-";
const CACHE_NAME = `${CACHE_PREFIX}v5`;
const SHELL_URL = "/";
const PUBLIC_PATHS = new Set([
  SHELL_URL,
  "/manifest.webmanifest",
  "/icon.svg",
  "/favicon.ico",
  "/apple-touch-icon.png",
  "/icon-192.png",
  "/icon-512.png",
  "/icon-mask.png",
  "/icon-monochrome.png",
]);

function isCacheable(response) {
  return response.ok && response.type !== "opaque";
}

function isHTMLResponse(response) {
  const contentType = response.headers.get("Content-Type") ?? "";
  const cacheControl = response.headers.get("Cache-Control") ?? "";
  const noStore = cacheControl
    .split(",")
    .some(
      (directive) =>
        directive.trim().split("=", 1)[0].toLowerCase() === "no-store",
    );
  return (
    isCacheable(response) &&
    !noStore &&
    /^text\/html(?:;|$)/i.test(contentType.trim())
  );
}

function isPublicAsset(request, url) {
  return (
    url.origin === self.location.origin &&
    request.method === "GET" &&
    (PUBLIC_PATHS.has(url.pathname) || url.pathname.startsWith("/assets/"))
  );
}

function isShellNavigation(request, url) {
  return (
    url.origin === self.location.origin &&
    request.method === "GET" &&
    request.mode === "navigate" &&
    !url.pathname.startsWith("/api/") &&
    url.pathname !== "/api"
  );
}

async function precacheShell() {
  const cache = await caches.open(CACHE_NAME);
  await cache.addAll([...PUBLIC_PATHS]);
  const visited = new Set();
  const pending = [SHELL_URL];

  while (pending.length > 0) {
    const path = pending.shift();
    if (!path || visited.has(path)) continue;
    visited.add(path);
    const response = await cache.match(path);
    const contentType = response?.headers.get("Content-Type") ?? "";
    if (!response || !/(?:html|css|javascript)/i.test(contentType)) continue;
    const source = await response.text();
    const assets = source.match(/\/assets\/[A-Za-z0-9._/-]+/g) ?? [];
    for (const asset of new Set(assets)) {
      if (visited.has(asset)) continue;
      await cache.add(asset);
      pending.push(asset);
    }
  }
}

async function refreshPublicAsset(request) {
  try {
    const response = await fetch(request);
    if (isCacheable(response)) {
      const cache = await caches.open(CACHE_NAME);
      await cache.put(request, response.clone());
    }
    return response;
  } catch (error) {
    const cache = await caches.open(CACHE_NAME);
    const cached = await cache.match(request);
    if (cached) return cached;
    throw error;
  }
}

async function refreshShell(request) {
  try {
    const response = await fetch(request);
    if (isHTMLResponse(response)) {
      const cache = await caches.open(CACHE_NAME);
      await cache.put(SHELL_URL, response.clone());
    }
    return response;
  } catch (error) {
    const cache = await caches.open(CACHE_NAME);
    const cached = await cache.match(SHELL_URL);
    if (cached) return cached;
    throw error;
  }
}

self.addEventListener("install", (event) => {
  event.waitUntil(precacheShell());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    Promise.all([
      caches
        .keys()
        .then((keys) =>
          Promise.all(
            keys
              .filter(
                (key) => key.startsWith(CACHE_PREFIX) && key !== CACHE_NAME,
              )
              .map((key) => caches.delete(key)),
          ),
        ),
      self.clients.claim(),
    ]),
  );
});

self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") {
    event.waitUntil(self.skipWaiting());
  }
});

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  if (isShellNavigation(event.request, url)) {
    event.respondWith(refreshShell(event.request));
    return;
  }
  if (isPublicAsset(event.request, url)) {
    event.respondWith(refreshPublicAsset(event.request));
  }
});
