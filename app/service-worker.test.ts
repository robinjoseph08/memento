import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import vm from "node:vm";

import { expect, test, vi } from "vitest";

type FetchEvent = {
  request: Request;
  respondWith(response: Promise<Response>): void;
};

type FetchHandler = (event: FetchEvent) => void;

async function loadFetchHandler() {
  const listeners = new Map<string, (event: never) => void>();
  const cache = {
    addAll: vi.fn(() => Promise.resolve()),
    put: vi.fn(() => Promise.resolve()),
  };
  const caches = {
    delete: vi.fn(() => Promise.resolve(true)),
    keys: vi.fn(() => Promise.resolve([] as string[])),
    match: vi.fn(() => Promise.resolve(undefined as Response | undefined)),
    open: vi.fn(() => Promise.resolve(cache)),
  };
  const fetchMock = vi.fn<typeof fetch>();
  const self = {
    location: { origin: "https://memento.example" },
    addEventListener(type: string, handler: (event: never) => void) {
      listeners.set(type, handler);
    },
  };
  const source = await readFile(
    resolve(process.cwd(), "public/service-worker.js"),
    "utf8",
  );
  vm.runInNewContext(source, { caches, fetch: fetchMock, self, URL });
  return {
    cache,
    caches,
    fetchMock,
    handler: listeners.get("fetch") as FetchHandler,
  };
}

test("service worker never handles protected API requests", async () => {
  const worker = await loadFetchHandler();
  const respondWith = vi.fn();

  worker.handler({
    request: new Request("https://memento.example/api/media/private"),
    respondWith,
  });

  expect(respondWith).not.toHaveBeenCalled();
  expect(worker.fetchMock).not.toHaveBeenCalled();
  expect(worker.caches.open).not.toHaveBeenCalled();
});

test("service worker refreshes public shell assets before using cached copies", async () => {
  const worker = await loadFetchHandler();
  const oldResponse = new Response("old shell");
  const newResponse = new Response("new shell");
  worker.caches.match.mockResolvedValue(oldResponse);
  worker.fetchMock.mockResolvedValue(newResponse);
  let responsePromise: Promise<Response> | undefined;

  worker.handler({
    request: new Request("https://memento.example/"),
    respondWith(response) {
      responsePromise = response;
    },
  });

  expect(responsePromise).toBeDefined();
  await expect(responsePromise).resolves.toBe(newResponse);
  expect(worker.fetchMock).toHaveBeenCalledOnce();
  expect(worker.caches.match).not.toHaveBeenCalled();
  expect(worker.cache.put).toHaveBeenCalledOnce();
});
