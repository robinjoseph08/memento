/* global caches, fetch, Request, self, URL */

const CACHE_PREFIX = "memento-shell-";
const BUILD_REVISION = "__MEMENTO_BUILD_REVISION__";
const CACHE_NAME = `${CACHE_PREFIX}${BUILD_REVISION}`;
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

function onlyKeys(value, allowed) {
  return Object.keys(value).every((key) => allowed.has(key));
}

async function pushActivitySummary(data) {
  if (!data) return undefined;
  try {
    const text = await data.text();
    if (text.length > 3500) return undefined;
    const payload = JSON.parse(text);
    if (
      !payload ||
      typeof payload !== "object" ||
      Array.isArray(payload) ||
      !onlyKeys(payload, new Set(["version", "activities", "truncated"])) ||
      payload.version !== 1 ||
      !Array.isArray(payload.activities) ||
      payload.activities.length < 1 ||
      payload.activities.length > 20 ||
      (payload.truncated !== undefined &&
        typeof payload.truncated !== "boolean")
    ) {
      return undefined;
    }
    const summaries = [];
    for (const activity of payload.activities) {
      if (
        !activity ||
        typeof activity !== "object" ||
        Array.isArray(activity)
      ) {
        return undefined;
      }
      if (activity.kind === "publication") {
        if (
          !onlyKeys(
            activity,
            new Set(["kind", "title", "addition_count", "count_capped"]),
          ) ||
          typeof activity.title !== "string" ||
          activity.title.length < 1 ||
          activity.title.length > 480 ||
          !Number.isInteger(activity.addition_count) ||
          activity.addition_count < 1 ||
          activity.addition_count > 100 ||
          (activity.count_capped !== undefined &&
            typeof activity.count_capped !== "boolean")
        ) {
          return undefined;
        }
        const count = `${activity.addition_count}${activity.count_capped ? "+" : ""}`;
        summaries.push(
          `${activity.title}: ${count} new ${activity.addition_count === 1 && !activity.count_capped ? "item" : "items"}.`,
        );
      } else if (activity.kind === "comment") {
        if (
          !onlyKeys(activity, new Set(["kind", "author"])) ||
          typeof activity.author !== "string" ||
          activity.author.length < 1 ||
          activity.author.length > 240
        ) {
          return undefined;
        }
        summaries.push(`${activity.author} commented on an item.`);
      } else {
        return undefined;
      }
    }
    const remaining = summaries.length - 1;
    if (remaining === 0) {
      return payload.truncated
        ? `${summaries[0]} More updates are ready.`
        : summaries[0];
    }
    const label =
      remaining === 1 && !payload.truncated ? "update is" : "updates are";
    return `${summaries[0]} ${remaining}${payload.truncated ? "+" : ""} more ${label} ready.`;
  } catch {
    return undefined;
  }
}

async function showPushNotification(data) {
  const summary = await pushActivitySummary(data);
  await self.registration.showNotification("New activity in Memento", {
    body: summary ?? "New Memento activity is ready.",
    tag: "memento-activity",
  });
}

async function openPhotos() {
  const destination = new URL("/photos", self.location.origin);
  const openClients = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  for (const client of openClients) {
    if (new URL(client.url).origin !== self.location.origin) continue;
    try {
      await client.navigate(destination.href);
      await client.focus();
      return;
    } catch {
      // Try another same-origin window before opening a new one.
    }
  }
  await self.clients.openWindow("/photos");
}

async function announceSubscriptionChange() {
  const openClients = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  for (const client of openClients) {
    if (new URL(client.url).origin === self.location.origin) {
      client.postMessage({ type: "PUSH_SUBSCRIPTION_CHANGED" });
    }
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

self.addEventListener("push", (event) => {
  event.waitUntil(showPushNotification(event.data));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(openPhotos());
});

self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(announceSubscriptionChange());
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
