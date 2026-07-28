import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import vm from "node:vm";

import { expect, test, vi } from "vitest";

type ExtendableEvent = {
  data?: unknown;
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
    add: vi.fn((request: RequestInfo | URL) => {
      void request;
      return Promise.resolve();
    }),
    addAll: vi.fn((requests: Array<RequestInfo | URL>) => {
      void requests;
      return Promise.resolve();
    }),
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
    clients: { claim: vi.fn(() => Promise.resolve()) },
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
  const oldResponse = new Response("old shell");
  const newResponse = new Response("new shell");
  worker.cache.match.mockResolvedValue(oldResponse);
  worker.fetchMock.mockResolvedValue(newResponse);
  const probe = fetchEvent(
    new Request("https://memento.example/assets/app.js"),
  );

  worker.listeners.get("fetch")!(probe.event);

  await expect(probe.response()).resolves.toBe(newResponse);
  expect(worker.fetchMock).toHaveBeenCalledOnce();
  expect(worker.cache.match).not.toHaveBeenCalled();
  expect(worker.cache.put).toHaveBeenCalledOnce();
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

test("successful token-bearing HTML navigation refreshes only the stable shell key", async () => {
  const worker = await loadWorker();
  const response = new Response("fresh shell", {
    headers: { "Content-Type": "text/html; charset=utf-8" },
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

  expect(worker.cache.put).toHaveBeenCalledOnce();
  expect(worker.cache.put.mock.calls[0][0]).toBe("/");
  expect(JSON.stringify(worker.cache.put.mock.calls)).not.toContain(
    "private-token",
  );
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
  let work: Promise<unknown> | undefined;

  worker.listeners.get("install")!({
    request: new Request("https://memento.example/"),
    respondWith: vi.fn(),
    waitUntil(promise) {
      work = promise;
    },
  });
  await work;

  expect(worker.cache.addAll).toHaveBeenCalledOnce();
  const requests = worker.cache.addAll.mock.calls[0][0] as Request[];
  const paths = requests.map((request) => new URL(request.url).pathname);
  expect(paths).toContain("/");
  expect(paths).toContain("/icon-512.png");
  expect(paths.every((path) => !path.startsWith("/api"))).toBe(true);
  expect(requests.every((request) => request.cache === "reload")).toBe(true);
  expect(worker.self.skipWaiting).not.toHaveBeenCalled();
});

test("install discovers hashed scripts, styles, and self-hosted fonts", async () => {
  const worker = await loadWorker();
  worker.cache.match.mockImplementation((path: RequestInfo | URL) => {
    if (path === "/") {
      return Promise.resolve(
        new Response(
          '<link href="/assets/app-123.css"><script src="/assets/app-456.js">',
          { headers: { "Content-Type": "text/html" } },
        ),
      );
    }
    if (path === "/assets/app-123.css") {
      return Promise.resolve(
        new Response('@font-face{src:url("/assets/dm-sans-789.woff2")}', {
          headers: { "Content-Type": "text/css" },
        }),
      );
    }
    return Promise.resolve(
      new Response("asset", {
        headers: { "Content-Type": "application/octet-stream" },
      }),
    );
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

  const added = worker.cache.add.mock.calls.map(
    ([request]) => request as Request,
  );
  expect(added.map((request) => new URL(request.url).pathname)).toEqual([
    "/assets/app-123.css",
    "/assets/app-456.js",
    "/assets/dm-sans-789.woff2",
  ]);
  expect(added.every((request) => request.cache === "reload")).toBe(true);
});

test("activation replaces only Memento shell caches and claims open clients", async () => {
  const worker = await loadWorker();
  worker.caches.keys.mockResolvedValue([
    "memento-shell-v5",
    "memento-shell-v6",
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
  expect(worker.caches.delete).toHaveBeenCalledWith("memento-shell-v5");
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
