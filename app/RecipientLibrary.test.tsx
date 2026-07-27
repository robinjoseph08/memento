import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { RecipientLibrary } from "./RecipientLibrary";

const session = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

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

function renderLibrary() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RecipientLibrary session={session} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("lands on Photos with durable New for you and real-ratio authorized thumbnails", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/me/photos?")) {
        return json({
          media: [
            {
              id: "media-1",
              media_type: "image",
              width: 1600,
              height: 900,
              local_date_time: "2026-07-27T12:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/media-1/thumbnail",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") {
        return json({
          events: [
            {
              id: "event-1",
              publication_id: "publication-1",
              title: "Family weekend",
              description: "Authorized only",
              committed_at: "2026-07-27T12:00:00Z",
              cover_media_id: "media-1",
              cover_width: 1600,
              cover_height: 900,
              cover_available: true,
              thumbnail_url: "/api/me/media/media-1/thumbnail",
              media_count: 1,
            },
          ],
        });
      }
      if (path.startsWith("/api/me/events/event-1?")) {
        return json({
          id: "event-1",
          publication_id: "publication-1",
          title: "Family weekend",
          description: "Authorized only",
          committed_at: "2026-07-27T12:00:00Z",
          cover_media_id: "media-1",
          media_count: 1,
          media: [],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you/publication-1/seen") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();

  expect(await screen.findByRole("heading", { name: "Photos" })).toBeVisible();
  expect(
    await screen.findByRole("heading", { name: "New for you" }),
  ).toBeVisible();
  const image = await screen.findByAltText("Authorized photo");
  expect(image).toHaveAttribute("src", "/api/me/media/media-1/thumbnail");
  expect(image.closest("figure")).toHaveStyle({ aspectRatio: "1600 / 900" });
  const newEvent = screen.getByRole("button", { name: /Family weekend/ });
  expect(newEvent).toHaveStyle({
    flexBasis: `${(1600 / 900) * 11}rem`,
    flexGrow: 1600 / 900,
  });
  expect(newEvent.querySelector(".event-cover")).toHaveStyle({
    aspectRatio: "1600 / 900",
  });

  fireEvent.click(newEvent);
  await screen.findByRole("heading", { name: "Family weekend" });
  await waitFor(() =>
    expect(
      requests.find(({ path }) => path.endsWith("publication-1/seen"))?.init,
    ).toMatchObject({
      method: "POST",
      headers: { "X-Memento-CSRF": session.csrf_token },
    }),
  );
});

test("navigates Events and Favorites without exposing an unavailable aggregate", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (
        path.startsWith("/api/me/photos?") ||
        path.startsWith("/api/me/favorites?")
      ) {
        return json({ media: [], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path.startsWith("/api/me/events?")) {
        return json({
          events: [
            {
              id: "event-1",
              publication_id: "publication-1",
              title: "One visible item",
              description: "",
              committed_at: "2026-07-27T12:00:00Z",
              cover_media_id: "media-1",
              cover_width: 900,
              cover_height: 1600,
              cover_available: true,
              thumbnail_url: "/api/me/media/media-1/thumbnail",
              media_count: 1,
            },
          ],
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  await screen.findByRole("heading", { name: "Photos" });
  fireEvent.click(screen.getAllByRole("button", { name: "Events" })[0]);
  expect(await screen.findByText("1 item")).toBeVisible();
  const event = screen.getByRole("button", { name: /One visible item/ });
  expect(event.querySelector(".event-cover")).toHaveStyle({
    aspectRatio: "900 / 1600",
  });
  expect(screen.queryByText(/total/i)).not.toBeInTheDocument();

  fireEvent.click(screen.getAllByRole("button", { name: "Favorites" })[0]);
  expect(
    await screen.findByText("Favorites aren't shared with other recipients."),
  ).toBeVisible();
  expect(await screen.findByText("No authorized Favorites yet.")).toBeVisible();
});
