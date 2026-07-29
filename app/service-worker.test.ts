import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import vm from "node:vm";

import { expect, test, vi } from "vitest";

type ExtendableEvent = {
  data?: unknown;
  notification?: { close(): void; data?: unknown };
  waitUntil(response: Promise<unknown>): void;
};

type FetchEvent = {
  request: Request;
  respondWith(response: Promise<Response>): void;
};

type WorkerHandler = (event: ExtendableEvent & FetchEvent) => void;

async function loadWorker() {
  const listeners = new Map<string, WorkerHandler>();
  const cache = {
    match: vi.fn((request: RequestInfo | URL, options?: CacheQueryOptions) => {
      void request;
      void options;
      return Promise.resolve(undefined as Response | undefined);
    }),
    put: vi.fn((request: RequestInfo | URL, response: Response) => {
      void request;
      void response;
      return Promise.resolve();
    }),
  };
  const caches = {
    delete: vi.fn(() => Promise.resolve(true)),
    keys: vi.fn(() => Promise.resolve([] as string[])),
    open: vi.fn(() => Promise.resolve(cache)),
  };
  const fetchMock = vi.fn<typeof fetch>();
  const self = {
    location: { origin: "https://memento.example" },
    clients: {
      claim: vi.fn(() => Promise.resolve()),
      matchAll: vi.fn(() => Promise.resolve([] as unknown[])),
      openWindow: vi.fn(() => Promise.resolve()),
    },
    registration: {
      showNotification: vi.fn(() => Promise.resolve()),
    },
    skipWaiting: vi.fn(() => Promise.resolve()),
    addEventListener(type: string, handler: WorkerHandler) {
      listeners.set(type, handler);
    },
  };
  const source = await readFile(
    resolve(process.cwd(), "public/service-worker.js"),
    "utf8",
  );
  vm.runInNewContext(source, {
    caches,
    fetch: fetchMock,
    Request,
    self,
    URL,
  });
  return { cache, caches, fetchMock, listeners, self };
}

function fetchEvent(request: Request) {
  let responsePromise: Promise<Response> | undefined;
  return {
    event: {
      request,
      respondWith(response: Promise<Response>) {
        responsePromise = response;
      },
    } as FetchEvent & ExtendableEvent,
    response: () => responsePromise,
  };
}

function publicResponse(path: string, body = "public asset") {
  let contentType = "application/octet-stream";
  if (path === "/") contentType = "text/html";
  else if (path === "/manifest.webmanifest")
    contentType = "application/manifest+json";
  else if (path.endsWith(".css")) contentType = "text/css";
  else if (path.endsWith(".ico")) contentType = "image/x-icon";
  else if (path.endsWith(".js")) contentType = "text/javascript";
  else if (path.endsWith(".png")) contentType = "image/png";
  else if (path.endsWith(".svg")) contentType = "image/svg+xml";
  else if (path.endsWith(".woff")) contentType = "font/woff";
  else if (path.endsWith(".woff2")) contentType = "font/woff2";
  return new Response(body, { headers: { "Content-Type": contentType } });
}

function mockPublicInstall(
  worker: Awaited<ReturnType<typeof loadWorker>>,
  overrides: Record<string, Response> = {},
) {
  worker.fetchMock.mockImplementation((request) => {
    const path = new URL(
      typeof request === "string" || request instanceof URL
        ? request.toString()
        : request.url,
    ).pathname;
    return Promise.resolve(overrides[path] ?? publicResponse(path));
  });
}

test.each([
  "https://memento.example/api/session",
  "https://memento.example/api/me/photos?limit=40",
  "https://memento.example/api/me/media/private/thumbnail",
  "https://memento.example/protected-media/family-photo.jpg",
  "https://memento.example/private-gallery/alex/library.json",
  "https://memento.example/archives/family-weekend.zip",
  "https://immich.example/api/assets/private",
])("service worker never handles protected request %s", async (url) => {
  const worker = await loadWorker();
  const probe = fetchEvent(new Request(url));

  worker.listeners.get("fetch")!(probe.event);

  expect(probe.response()).toBeUndefined();
  expect(worker.fetchMock).not.toHaveBeenCalled();
  expect(worker.caches.open).not.toHaveBeenCalled();
});

test("service worker refreshes public build assets before using cached copies", async () => {
  const worker = await loadWorker();
  const oldResponse = publicResponse("/assets/app.js", "old shell");
  const newResponse = publicResponse("/assets/app.js", "new shell");
  worker.cache.match.mockResolvedValue(oldResponse);
  worker.fetchMock.mockResolvedValue(newResponse);
  const probe = fetchEvent(
    new Request("https://memento.example/assets/app.js"),
  );

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(newResponse);
  expect(worker.fetchMock).toHaveBeenCalledOnce();
  expect(worker.cache.match).toHaveBeenCalledWith("/assets/app.js", {
    ignoreVary: true,
  });
  expect(worker.cache.put).toHaveBeenCalledWith(
    "/assets/app.js",
    expect.any(Response),
  );
});

test.each([
  "https://memento.example/icon.svg?recipient=alex",
  "https://memento.example/assets/app.js?token=private",
])("service worker rejects query-bearing public asset %s", async (url) => {
  const worker = await loadWorker();
  const probe = fetchEvent(new Request(url));

  worker.listeners.get("fetch")!(probe.event);

  expect(probe.response()).toBeUndefined();
  expect(worker.fetchMock).not.toHaveBeenCalled();
  expect(worker.caches.open).not.toHaveBeenCalled();
});

test("service worker does not cache an undiscovered build asset", async () => {
  const worker = await loadWorker();
  const response = publicResponse("/assets/private-export.js");
  worker.fetchMock.mockResolvedValue(response);
  const probe = fetchEvent(
    new Request("https://memento.example/assets/private-export.js"),
  );

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(response);
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test.each([
  {
    name: "no-store response",
    response: new Response("icon", {
      headers: {
        "Cache-Control": "public, No-Store",
        "Content-Type": "image/svg+xml",
      },
    }),
  },
  {
    name: "mismatched content type",
    response: new Response("<html>fallback</html>", {
      headers: { "Content-Type": "text/html" },
    }),
  },
])("service worker does not cache a $name", async ({ response }) => {
  const worker = await loadWorker();
  worker.fetchMock.mockResolvedValue(response);
  const probe = fetchEvent(new Request("https://memento.example/icon.svg"));

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(response);
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("standalone navigation falls back to the cached shell without storing its URL", async () => {
  const worker = await loadWorker();
  const cached = new Response("cached shell");
  worker.fetchMock.mockRejectedValue(new TypeError("offline"));
  worker.cache.match.mockResolvedValue(cached);
  const navigation = {
    method: "GET",
    mode: "navigate",
    url: "https://memento.example/photos?from=home-screen",
  } as Request;
  const probe = fetchEvent(navigation);

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(cached);
  expect(worker.cache.match).toHaveBeenCalledWith("/");
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test.each([
  "https://memento.example/invitation?token=private-token",
  "https://memento.example/protected-media/private-report",
])("noncanonical HTML navigation %s cannot replace the shell", async (url) => {
  const worker = await loadWorker();
  const response = new Response("private HTML", {
    headers: { "Content-Type": "text/html; charset=utf-8" },
  });
  worker.fetchMock.mockResolvedValue(response);
  const navigation = {
    method: "GET",
    mode: "navigate",
    url,
  } as Request;
  const probe = fetchEvent(navigation);

  worker.listeners.get("fetch")!(probe.event);
  await expect(probe.response()).resolves.toBe(response);

  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("canonical root HTML navigation refreshes the stable shell key", async () => {
  const worker = await loadWorker();
  const response = new Response("fresh shell", {
    headers: { "Content-Type": "text/html; charset=utf-8" },
  });
  worker.fetchMock.mockResolvedValue(response);
  const navigation = {
    method: "GET",
    mode: "navigate",
    url: "https://memento.example/",
  } as Request;
  const probe = fetchEvent(navigation);

  worker.listeners.get("fetch")!(probe.event);
  await expect(probe.response()).resolves.toBe(response);

  expect(worker.cache.put).toHaveBeenCalledOnce();
  expect(worker.cache.put.mock.calls[0][0]).toBe("/");
});

test("no-store HTML navigation does not replace the cached application shell", async () => {
  const worker = await loadWorker();
  const response = new Response("private shell", {
    headers: {
      "Cache-Control": "private, No-Store",
      "Content-Type": "text/html; charset=utf-8",
    },
  });
  worker.fetchMock.mockResolvedValue(response);
  const navigation = {
    method: "GET",
    mode: "navigate",
    url: "https://memento.example/invitation?token=private-token",
  } as Request;
  const probe = fetchEvent(navigation);

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(response);
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("non-HTML navigation never replaces the cached application shell", async () => {
  const worker = await loadWorker();
  const response = new Response("download", {
    headers: { "Content-Type": "application/octet-stream" },
  });
  worker.fetchMock.mockResolvedValue(response);
  const navigation = {
    method: "GET",
    mode: "navigate",
    url: "https://memento.example/export",
  } as Request;
  const probe = fetchEvent(navigation);

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(response);
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("service worker falls back only to a cached public asset", async () => {
  const worker = await loadWorker();
  const cached = new Response("cached asset");
  worker.fetchMock.mockRejectedValue(new TypeError("offline"));
  worker.cache.match.mockResolvedValue(cached);
  const probe = fetchEvent(new Request("https://memento.example/icon.svg"));

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(cached);
  expect(worker.cache.match).toHaveBeenCalledOnce();
  expect(worker.cache.match).toHaveBeenCalledWith("/icon.svg", {
    ignoreVary: true,
  });
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("service worker reports an unavailable uncached public asset", async () => {
  const worker = await loadWorker();
  worker.fetchMock.mockRejectedValue(new TypeError("offline"));
  const probe = fetchEvent(new Request("https://memento.example/icon.svg"));

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).rejects.toThrow("offline");
});

test("install preloads only the public shell and selected Memento icons", async () => {
  const worker = await loadWorker();
  mockPublicInstall(worker);
  let work: Promise<unknown> | undefined;

  worker.listeners.get("install")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  const requests = worker.fetchMock.mock.calls.map(
    ([request]) => request as Request,
  );
  const paths = requests.map((request) => new URL(request.url).pathname);
  expect(paths).toContain("/");
  expect(paths).toContain("/icon-512.png");
  expect(paths.every((path) => !path.startsWith("/api"))).toBe(true);
  expect(requests.every((request) => request.cache === "reload")).toBe(true);
  expect(worker.cache.put).toHaveBeenCalledTimes(paths.length);
  expect(worker.self.skipWaiting).not.toHaveBeenCalled();
});

test("install discovers hashed scripts, styles, and self-hosted fonts", async () => {
  const worker = await loadWorker();
  mockPublicInstall(worker, {
    "/": publicResponse(
      "/",
      '<link href="/assets/app-123.css"><script src="/assets/app-456.js">',
    ),
    "/assets/app-123.css": publicResponse(
      "/assets/app-123.css",
      '@font-face{src:url("/assets/dm-sans-789.woff2")}',
    ),
  });
  let work: Promise<unknown> | undefined;

  worker.listeners.get("install")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  const fetched = worker.fetchMock.mock.calls.map(
    ([request]) => request as Request,
  );
  const fetchedPaths = fetched.map((request) => new URL(request.url).pathname);
  expect(fetchedPaths).toEqual(
    expect.arrayContaining([
      "/assets/app-123.css",
      "/assets/app-456.js",
      "/assets/dm-sans-789.woff2",
    ]),
  );
  expect(fetched.every((request) => request.cache === "reload")).toBe(true);
  const cachedPaths = worker.cache.put.mock.calls.map(([path]) => path);
  expect(cachedPaths).toEqual(
    expect.arrayContaining([
      "/assets/app-123.css",
      "/assets/app-456.js",
      "/assets/dm-sans-789.woff2",
    ]),
  );
});

test.each([
  {
    name: "SPA HTML for JavaScript",
    response: new Response("<html>fallback</html>", {
      headers: { "Content-Type": "text/html" },
    }),
  },
  {
    name: "an unsuccessful stylesheet response",
    response: new Response("missing", {
      status: 404,
      headers: { "Content-Type": "text/css" },
    }),
  },
  {
    name: "a no-store build response",
    response: new Response("private build", {
      headers: {
        "Cache-Control": "private, no-store",
        "Content-Type": "text/javascript",
      },
    }),
  },
])(
  "install rejects $name without committing a false-ready cache",
  async ({ response }) => {
    const worker = await loadWorker();
    mockPublicInstall(worker, {
      "/": publicResponse(
        "/",
        '<script src="/assets/app-456.js"></script><link href="/assets/app-123.css">',
      ),
      "/assets/app-456.js": response,
      "/assets/app-123.css": response,
    });
    let work: Promise<unknown> | undefined;

    worker.listeners.get("install")!({
      request: new Request("https://memento.example/"),
      respondWith: vi.fn(),
      waitUntil(promise) {
        work = promise;
      },
    });

    await expect(work).rejects.toThrow(
      "refusing invalid public asset response",
    );
    expect(worker.cache.put).not.toHaveBeenCalled();
  },
);

test("install rejects query-bearing discovered assets", async () => {
  const worker = await loadWorker();
  mockPublicInstall(worker, {
    "/": publicResponse(
      "/",
      '<script src="/assets/app-456.js?token=private"></script>',
    ),
  });
  let work: Promise<unknown> | undefined;

  worker.listeners.get("install")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });

  await expect(work).rejects.toThrow("refusing noncanonical public asset");
  expect(worker.cache.put).not.toHaveBeenCalled();
});

test("activation replaces only Memento shell caches and claims open clients", async () => {
  const worker = await loadWorker();
  worker.caches.keys.mockResolvedValue([
    "memento-shell-prior-revision",
    "memento-shell-__MEMENTO_BUILD_REVISION__",
    "another-application-cache",
  ]);
  let work: Promise<unknown> | undefined;

  worker.listeners.get("activate")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(worker.caches.delete).toHaveBeenCalledOnce();
  expect(worker.caches.delete).toHaveBeenCalledWith(
    "memento-shell-prior-revision",
  );
  expect(worker.self.clients.claim).toHaveBeenCalledOnce();
});

test("an explicit update message activates a waiting worker", async () => {
  const worker = await loadWorker();
  let work: Promise<unknown> | undefined;

  worker.listeners.get("message")!({
    data: { type: "SKIP_WAITING" },
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(worker.self.skipWaiting).toHaveBeenCalledOnce();
});

test("push shows authorized Publication and Comment context from a bounded payload", async () => {
  const worker = await loadWorker();
  let work: Promise<unknown> | undefined;

  worker.listeners.get("push")!({
    data: {
      text: () =>
        Promise.resolve(
          '{"version":1,"activities":[{"kind":"publication","title":"Family picnic","addition_count":3},{"kind":"comment","author":"Blair"}]}',
        ),
    },
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(worker.self.registration.showNotification).toHaveBeenCalledOnce();
  expect(worker.self.registration.showNotification).toHaveBeenCalledWith(
    "New activity in Memento",
    {
      body: "Family picnic: 3 new items. 1 more update is ready.",
      tag: "memento-activity",
    },
  );
  const notification = JSON.stringify(
    worker.self.registration.showNotification.mock.calls[0],
  );
  expect(notification).not.toMatch(/media|thumbnail|https?:|\/photos/i);
});

test.each([
  undefined,
  '{"version":2,"activities":[{"kind":"comment","author":"Alex"}]}',
  '{"version":1,"activities":[]}',
  '{"version":1,"activities":[{"kind":"publication","title":"Family","addition_count":101}]}',
  '{"version":1,"activities":[{"kind":"comment","author":"Alex","url":"/photos"}]}',
  "not-json",
])(
  "invalid or absent push data still shows one generic notification",
  async (text) => {
    const worker = await loadWorker();
    let work: Promise<unknown> | undefined;

    worker.listeners.get("push")!({
      data:
        text === undefined ? undefined : { text: () => Promise.resolve(text) },
      request: new Request("https://memento.example/"),
      respondWith: vi.fn(),
      waitUntil(promise) {
        work = promise;
      },
    });
    await work;

    expect(worker.self.registration.showNotification).toHaveBeenCalledOnce();
    expect(worker.self.registration.showNotification).toHaveBeenCalledWith(
      "New activity in Memento",
      expect.objectContaining({ body: "New Memento activity is ready." }),
    );
  },
);

test("notification click ignores notification data and navigates a same-origin client to photos", async () => {
  const worker = await loadWorker();
  const client = {
    url: "https://memento.example/private-location",
    navigate: vi.fn(() => Promise.resolve()),
    focus: vi.fn(() => Promise.resolve()),
    postMessage: vi.fn(),
  };
  worker.self.clients.matchAll.mockResolvedValue([client]);
  const close = vi.fn();
  let work: Promise<unknown> | undefined;

  worker.listeners.get("notificationclick")!({
    notification: {
      close,
      data: { url: "https://attacker.example/private-media" },
    },
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(close).toHaveBeenCalledOnce();
  expect(client.navigate).toHaveBeenCalledWith(
    "https://memento.example/photos",
  );
  expect(client.focus).toHaveBeenCalledOnce();
  expect(worker.self.clients.openWindow).not.toHaveBeenCalled();
});

test("notification click opens the hardcoded photos destination without a usable client", async () => {
  const worker = await loadWorker();
  worker.self.clients.matchAll.mockResolvedValue([]);
  let work: Promise<unknown> | undefined;

  worker.listeners.get("notificationclick")!({
    notification: { close: vi.fn() },
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(worker.self.clients.openWindow).toHaveBeenCalledWith("/photos");
});

test("push subscription changes ask same-origin clients to reconcile without subscribing", async () => {
  const worker = await loadWorker();
  const sameOrigin = {
    url: "https://memento.example/photos",
    postMessage: vi.fn(),
  };
  const foreign = {
    url: "https://elsewhere.example/",
    postMessage: vi.fn(),
  };
  worker.self.clients.matchAll.mockResolvedValue([sameOrigin, foreign]);
  let work: Promise<unknown> | undefined;

  worker.listeners.get("pushsubscriptionchange")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(sameOrigin.postMessage).toHaveBeenCalledWith({
    type: "PUSH_SUBSCRIPTION_CHANGED",
  });
  expect(foreign.postMessage).not.toHaveBeenCalled();
  const source = await readFile(
    resolve(process.cwd(), "public/service-worker.js"),
    "utf8",
  );
  expect(source).not.toContain("pushManager.subscribe");
});
