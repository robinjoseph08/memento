/* global caches, fetch, self, URL */

const CACHE_NAME = "memento-shell-v3";
const PUBLIC_PATHS = new Set([
  "/",
  "/manifest.webmanifest",
  "/icon.svg",
  "/favicon.ico",
  "/apple-touch-icon.png",
  "/icon-192.png",
  "/icon-512.png",
  "/icon-mask.png",
  "/icon-monochrome.png",
]);

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll([...PUBLIC_PATHS])),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((key) => key !== CACHE_NAME)
            .map((key) => caches.delete(key)),
        ),
      ),
  );
});

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  const isPublicAsset =
    url.origin === self.location.origin &&
    event.request.method === "GET" &&
    (PUBLIC_PATHS.has(url.pathname) || url.pathname.startsWith("/assets/"));
  if (!isPublicAsset) return;

  event.respondWith(
    fetch(event.request)
      .then(async (response) => {
        if (response.ok) {
          const cache = await caches.open(CACHE_NAME);
          await cache.put(event.request, response.clone());
        }
        return response;
      })
      .catch(async () => {
        const cached = await caches.match(event.request);
        if (!cached) throw new Error("public asset is unavailable");
        return cached;
      }),
  );
});
