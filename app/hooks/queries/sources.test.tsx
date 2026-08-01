import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, expect, test, vi } from "vitest";

import { sourceKeys } from "./curationKeys";
import { useSourceMedia } from "./sources";

const identityGeneration = "session-generation";

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.pathname;
  return new URL(input.url).pathname;
}

function queryHarness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

afterEach(() => vi.unstubAllGlobals());

test("Source Media selection keys include every Source and deduplicate stable Media identities", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const path = requestPath(input);
    const shared = {
      id: "media-shared",
      media_type: "image",
      width: 1200,
      height: 800,
      local_date_time: "2026-06-01T12:00:00",
    };
    const unique = {
      id: "media-undated",
      media_type: "video",
      width: null,
      height: null,
      local_date_time: null,
    };
    return Promise.resolve(
      new Response(
        JSON.stringify({
          media_items:
            path === "/api/sources/source-1/media-items"
              ? [shared]
              : [shared, unique],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  const { client, wrapper } = queryHarness();
  const { result } = renderHook(
    () => useSourceMedia(identityGeneration, ["source-1", "source-2"]),
    { wrapper },
  );

  await waitFor(() => expect(result.current.isPending).toBe(false));

  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(result.current.mediaItems.map((item) => item.id)).toEqual([
    "media-shared",
    "media-undated",
  ]);
  expect(
    client.getQueryData(
      sourceKeys.mediaSelection(identityGeneration, ["source-1", "source-2"]),
    ),
  ).toMatchObject({
    media_items: [{ id: "media-shared" }, { id: "media-undated" }],
  });
});

test("Source Media selection bounds concurrent Source requests", async () => {
  let active = 0;
  let maximumActive = 0;
  const pending: Array<() => void> = [];
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const path = requestPath(input);
    active += 1;
    maximumActive = Math.max(maximumActive, active);
    return new Promise<Response>((resolve) => {
      pending.push(() => {
        active -= 1;
        resolve(
          new Response(
            JSON.stringify({
              media_items: [
                {
                  id: path.split("/")[3],
                  media_type: "image",
                  width: 100,
                  height: 100,
                  local_date_time: null,
                },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      });
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  const { wrapper } = queryHarness();
  const sourceIDs = Array.from({ length: 6 }, (_, index) => `source-${index}`);
  const { result } = renderHook(
    () => useSourceMedia(identityGeneration, sourceIDs),
    { wrapper },
  );

  await waitFor(() => expect(pending).toHaveLength(4));
  expect(maximumActive).toBe(4);
  for (const resolve of pending.splice(0)) resolve();
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));
  for (const resolve of pending.splice(0)) resolve();
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(maximumActive).toBe(4);
  expect(result.current.mediaItems).toHaveLength(6);
});
