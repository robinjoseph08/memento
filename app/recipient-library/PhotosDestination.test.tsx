import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { RecipientLibrary } from "../RecipientLibrary";

const session = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

const scrollIntoView = vi.fn();

const chronology = {
  dates: [
    { capture_date: "2026-07-27", media_count: 1, cursor: "latest-anchor" },
    { capture_date: "2022-02-03", media_count: 2, cursor: "distant-anchor" },
    { capture_date: "2021-01-02", media_count: 1, cursor: "older-anchor" },
    { capture_date: null, media_count: 1, cursor: "undated-anchor" },
  ],
};

function media(id: string, captureDate: string | null) {
  return {
    id,
    media_type: "image",
    width: 1600,
    height: 900,
    capture_date: captureDate,
    local_date_time: captureDate ? `${captureDate}T12:00:00Z` : null,
    available: true,
    thumbnail_url: `/api/me/media/${id}/thumbnail`,
    preview_url: `/api/me/media/${id}/preview`,
    video_url: "",
    original_url: `/api/me/media/${id}/original`,
  };
}

function json(value: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.href;
  return input.url;
}

function CurrentRoute() {
  const location = useLocation();
  return (
    <output aria-label="Current route">
      {location.pathname}
      {location.search}
    </output>
  );
}

function stubScrollIntoView() {
  scrollIntoView.mockClear();
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: scrollIntoView,
  });
}

function renderLibrary(initialRoute: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[initialRoute]}>
      <QueryClientProvider client={client}>
        <RecipientLibrary session={session} />
        <CurrentRoute />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
});

test("opens a bookmarkable distant date at its chronology anchor and supports an undated mobile jump", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  stubScrollIntoView();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/me/photos/chronology") return json(chronology);
      if (path.includes("cursor=distant-anchor")) {
        return json({
          media: [media("distant", "2022-02-03")],
          next_cursor: null,
        });
      }
      if (path.includes("cursor=undated-anchor")) {
        return json({ media: [media("undated", null)], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/me/engagement") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary("/photos?date=2022-02-03");

  expect(
    await screen.findByRole("heading", { name: "February 3, 2022" }),
  ).toBeVisible();
  expect(screen.getByLabelText("Current route")).toHaveTextContent(
    "/photos?date=2022-02-03",
  );
  expect(
    requests.some(({ path }) => path.includes("cursor=latest-anchor")),
  ).toBe(false);
  const chronologyRequest = requests.find(
    ({ path }) => path === "/api/me/photos/chronology",
  );
  expect(chronologyRequest?.init?.signal).toBeInstanceOf(AbortSignal);
  const anchored = requests.find(({ path }) =>
    path.includes("cursor=distant-anchor"),
  );
  expect(anchored?.init?.signal).toBeInstanceOf(AbortSignal);
  expect(scrollIntoView).toHaveBeenCalledTimes(1);

  fireEvent.change(screen.getByRole("combobox", { name: "Jump to date" }), {
    target: { value: "undated" },
  });

  expect(
    await screen.findByRole("heading", { name: "Date unavailable" }),
  ).toBeVisible();
  expect(screen.getByLabelText("Current route")).toHaveTextContent(
    "/photos?date=undated",
  );
  expect(
    requests.some(({ path }) => path.includes("cursor=undated-anchor")),
  ).toBe(true);
});

test("refreshes chronology and replaces a withdrawn bookmark with the resolved date", async () => {
  let chronologyRequests = 0;
  stubScrollIntoView();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/me/photos/chronology") {
        chronologyRequests++;
        return json(
          chronologyRequests === 1
            ? {
                dates: [
                  {
                    capture_date: "2026-07-27",
                    media_count: 1,
                    cursor: "withdrawn-anchor",
                  },
                  {
                    capture_date: "2022-02-03",
                    media_count: 1,
                    cursor: "current-anchor",
                  },
                ],
              }
            : {
                dates: [
                  {
                    capture_date: "2022-02-03",
                    media_count: 1,
                    cursor: "current-anchor",
                  },
                ],
              },
        );
      }
      if (
        path.includes("cursor=withdrawn-anchor") ||
        path.includes("cursor=current-anchor")
      ) {
        return json({
          media: [media("current", "2022-02-03")],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/me/engagement") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary("/photos?date=2026-07-27");

  expect(
    await screen.findByRole("heading", { name: "February 3, 2022" }),
  ).toBeVisible();
  await waitFor(() => {
    expect(chronologyRequests).toBe(2);
    expect(screen.getByLabelText("Current route")).toHaveTextContent(
      "/photos?date=2022-02-03",
    );
  });
});

test("keeps the visible date marker when an older page is appended", async () => {
  type ObserverCallback = IntersectionObserverCallback;
  const callbacks: ObserverCallback[] = [];
  class TestIntersectionObserver {
    readonly root = null;
    readonly rootMargin = "0px";
    readonly thresholds = [0];
    observe = vi.fn();
    unobserve = vi.fn();
    disconnect = vi.fn();
    takeRecords = () => [];

    constructor(callback: ObserverCallback) {
      callbacks.push(callback);
    }
  }
  vi.stubGlobal("IntersectionObserver", TestIntersectionObserver);
  stubScrollIntoView();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/me/photos/chronology") return json(chronology);
      if (path.includes("cursor=distant-anchor")) {
        return json({
          media: [
            media("target", "2022-02-03"),
            media("visible", "2021-01-02"),
          ],
          next_cursor: "next-page",
        });
      }
      if (path.includes("cursor=next-page")) {
        return json({ media: [media("undated", null)], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/me/engagement") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary("/photos?date=2022-02-03");
  await screen.findByRole("heading", { name: "January 2, 2021" });
  const visibleSection = screen
    .getByRole("heading", { name: "January 2, 2021" })
    .closest("section");
  expect(visibleSection).not.toBeNull();

  act(() => {
    callbacks.at(-1)?.(
      [
        {
          isIntersecting: true,
          target: visibleSection as Element,
          boundingClientRect: { top: 20 },
        } as IntersectionObserverEntry,
      ],
      {} as IntersectionObserver,
    );
  });
  expect(screen.getByRole("slider", { name: "Photo dates" })).toHaveAttribute(
    "aria-valuetext",
    "January 2, 2021, 1 photo",
  );

  fireEvent.click(screen.getByRole("button", { name: "Load more photos" }));
  await screen.findByRole("heading", { name: "Date unavailable" });
  await waitFor(() =>
    expect(screen.getByRole("slider", { name: "Photo dates" })).toHaveAttribute(
      "aria-valuetext",
      "January 2, 2021, 1 photo",
    ),
  );
});
