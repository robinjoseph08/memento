/* global caches, fetch, Request, self, URL */

const CACHE_PREFIX = "memento-shell-";
const CACHE_NAME = `${CACHE_PREFIX}v7`;
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

const CONTENT_TYPES = new Map([
  ["/", new Set(["text/html"])],
  [
    "/manifest.webmanifest",
    new Set(["application/manifest+json", "application/json"]),
  ],
  [".css", new Set(["text/css"])],
  [".ico", new Set(["image/x-icon", "image/vnd.microsoft.icon", "image/ico"])],
  [
    ".js",
    new Set([
      "application/javascript",
      "application/x-javascript",
      "text/javascript",
    ]),
  ],
  [".png", new Set(["image/png"])],
  [".svg", new Set(["image/svg+xml"])],
  [".woff", new Set(["font/woff", "application/font-woff"])],
  [".woff2", new Set(["font/woff2"])],
]);
const BUILD_ASSET_PATH = /^\/assets\/[A-Za-z0-9._/-]+\.(?:css|js|woff|woff2)$/i;

function hasNoStore(response) {
  const cacheControl = response.headers.get("Cache-Control") ?? "";
  return cacheControl
    .split(",")
    .some(
      (directive) =>
        directive.trim().split("=", 1)[0].toLowerCase() === "no-store",
    );
}

function expectedContentTypes(path) {
  if (CONTENT_TYPES.has(path)) return CONTENT_TYPES.get(path);
  const extension = /\.[A-Za-z0-9]+$/.exec(path)?.[0].toLowerCase();
  return extension ? CONTENT_TYPES.get(extension) : undefined;
}

function isSupportedPublicPath(path) {
  return PUBLIC_PATHS.has(path) || BUILD_ASSET_PATH.test(path);
}

function isCacheablePublicResponse(path, response) {
  const contentType = (response.headers.get("Content-Type") ?? "")
    .split(";", 1)[0]
    .trim()
    .toLowerCase();
  const acceptedContentTypes = expectedContentTypes(path);
  return (
    isSupportedPublicPath(path) &&
    response.ok &&
    response.type !== "opaque" &&
    !hasNoStore(response) &&
    Boolean(acceptedContentTypes?.has(contentType))
  );
}

function isHTMLResponse(response) {
  return isCacheablePublicResponse(SHELL_URL, response);
}

function isPublicAsset(request, url) {
  return (
    url.origin === self.location.origin &&
    request.method === "GET" &&
    url.search === "" &&
    isSupportedPublicPath(url.pathname)
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

function freshRequest(path) {
  return new Request(new URL(path, self.location.origin), { cache: "reload" });
}

async function fetchInstallAsset(path) {
  const response = await fetch(freshRequest(path));
  if (!isCacheablePublicResponse(path, response)) {
    throw new Error(`refusing invalid public asset response for ${path}`);
  }
  return response;
}

async function precacheShell() {
  const staged = new Map();
  for (const path of PUBLIC_PATHS) {
    staged.set(path, await fetchInstallAsset(path));
  }

  const visited = new Set();
  const pending = [SHELL_URL];
  while (pending.length > 0) {
    const path = pending.shift();
    if (!path || visited.has(path)) continue;
    visited.add(path);
    const response = staged.get(path);
    const contentType = response.headers.get("Content-Type") ?? "";
    if (!/(?:html|css|javascript)/i.test(contentType)) continue;
    const source = await response.clone().text();
    const references =
      source.match(/\/assets\/[A-Za-z0-9._/-]+(?:\?[^\s"'()]*)?/g) ?? [];
    for (const reference of new Set(references)) {
      const asset = new URL(reference, self.location.origin);
      if (asset.search !== "" || !BUILD_ASSET_PATH.test(asset.pathname)) {
        throw new Error(`refusing noncanonical public asset ${reference}`);
      }
      if (staged.has(asset.pathname)) continue;
      staged.set(asset.pathname, await fetchInstallAsset(asset.pathname));
      pending.push(asset.pathname);
    }
  }

  const cache = await caches.open(CACHE_NAME);
  await Promise.all(
    [...staged].map(([path, response]) => cache.put(path, response)),
  );
}

async function refreshPublicAsset(request) {
  const path = new URL(request.url).pathname;
  const cache = await caches.open(CACHE_NAME);
  const cached = await cache.match(path, { ignoreVary: true });
  try {
    const response = await fetch(request);
    const isKnownAsset = PUBLIC_PATHS.has(path) || Boolean(cached);
    if (isKnownAsset && isCacheablePublicResponse(path, response)) {
      await cache.put(path, response.clone());
    }
    return response;
  } catch (error) {
    if (cached) return cached;
    throw error;
  }
}

async function refreshShell(request) {
  try {
    const response = await fetch(request);
    const url = new URL(request.url);
    if (
      url.pathname === SHELL_URL &&
      url.search === "" &&
      isHTMLResponse(response)
    ) {
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
