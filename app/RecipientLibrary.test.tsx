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

function apiError(message: string) {
  return Promise.resolve(
    new Response(JSON.stringify({ error: { message } }), {
      status: 503,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.href;
  return input.url;
}

function archiveRequest(init?: RequestInit) {
  if (typeof init?.body !== "string") {
    throw new Error("Expected a JSON archive request body.");
  }
  return JSON.parse(init.body) as {
    scope: "event" | "subset";
    event_id: string | null;
    media_ids: string[];
  };
}

function renderLibrary(librarySession = session) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RecipientLibrary session={librarySession} />
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
        if (path.includes("cursor=photos-next")) {
          return json({
            media: [
              {
                id: "media-2",
                media_type: "video",
                width: 900,
                height: 1600,
                local_date_time: "2026-07-27T13:00:00Z",
                available: true,
                thumbnail_url: "/api/me/media/media-2/thumbnail",
                preview_url: "/api/me/media/media-2/preview",
                video_url: "/api/me/media/media-2/video",
                original_url: "/api/me/media/media-2/original",
              },
            ],
            next_cursor: null,
          });
        }
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
              preview_url: "/api/me/media/media-1/preview",
              video_url: "",
              original_url: "/api/me/media/media-1/original",
            },
          ],
          next_cursor: "photos-next",
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
          media: [
            {
              id: "event-media-1",
              media_type: "image",
              width: 1200,
              height: 800,
              local_date_time: "2026-07-26T12:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/event-media-1/thumbnail",
              preview_url: "/api/me/media/event-media-1/preview",
              video_url: "",
              original_url: "/api/me/media/event-media-1/original",
            },
          ],
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
    screen.getAllByRole("navigation", { name: "Library navigation" }),
  ).toHaveLength(2);
  expect(
    await screen.findByRole("heading", { name: "New for you" }),
  ).toBeVisible();
  const image = await screen.findByAltText("Photo 1 from July 2026");
  expect(image).toHaveAttribute("src", "/api/me/media/media-1/thumbnail");
  expect(image.closest("figure")).toHaveStyle({ aspectRatio: "1600 / 900" });
  fireEvent.click(
    screen.getByRole("button", { name: "Open Photo 1 from July 2026" }),
  );
  expect(
    await screen.findByRole("dialog", { name: "Media viewer" }),
  ).toBeVisible();
  expect(screen.getByAltText("Selected photo preview")).toHaveAttribute(
    "src",
    "/api/me/media/media-1/preview",
  );
  expect(
    screen.getByRole("link", { name: "Download original" }),
  ).toHaveAttribute("href", "/api/me/media/media-1/original");
  expect(
    screen.queryByText(
      "This original will remain on this public computer after sign-out.",
    ),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Close viewer" }));
  expect(
    screen.queryByRole("dialog", { name: "Media viewer" }),
  ).not.toBeInTheDocument();
  const newEvent = screen.getByRole("button", { name: /Family weekend/ });
  expect(newEvent).toHaveStyle({
    flexBasis: `${(1600 / 900) * 11}rem`,
    flexGrow: 1600 / 900,
  });
  expect(newEvent.querySelector(".event-cover")).toHaveStyle({
    aspectRatio: "1600 / 900",
  });

  fireEvent.click(screen.getByRole("button", { name: "Load more photos" }));
  const appended = await screen.findByAltText("Video 2 from July 2026");
  expect(appended).toHaveAttribute("src", "/api/me/media/media-2/thumbnail");
  expect(screen.getByAltText("Photo 1 from July 2026")).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Open Video 2 from July 2026" }),
  );
  expect(screen.getByLabelText("Video preview")).toHaveAttribute(
    "src",
    "/api/me/media/media-2/video",
  );
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByLabelText("Video preview")).not.toBeInTheDocument();
  expect(requests.some(({ path }) => path.includes("cursor=photos-next"))).toBe(
    true,
  );

  fireEvent.click(newEvent);
  await screen.findByRole("heading", { name: "Family weekend" });
  expect(await screen.findByText("1 item")).toBeVisible();
  expect(screen.queryByText("1 items")).not.toBeInTheDocument();
  const eventThumbnail = await screen.findByAltText("Photo 1 from July 2026");
  expect(eventThumbnail).toHaveAttribute(
    "src",
    "/api/me/media/event-media-1/thumbnail",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Open Photo 1 from July 2026" }),
  );
  expect(screen.getByAltText("Selected photo preview")).toHaveAttribute(
    "src",
    "/api/me/media/event-media-1/preview",
  );
  expect(
    screen.getByRole("link", { name: "Download original" }),
  ).toHaveAttribute("href", "/api/me/media/event-media-1/original");
  await waitFor(() =>
    expect(
      requests.find(({ path }) => path.endsWith("publication-1/seen"))?.init,
    ).toMatchObject({
      method: "POST",
      headers: { "X-Memento-CSRF": session.csrf_token },
    }),
  );
});

test("shows the original download warning only for public computers", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
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
              preview_url: "/api/me/media/media-1/preview",
              video_url: "",
              original_url: "/api/me/media/media-1/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary({ ...session, session_type: "public" });
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );

  expect(
    screen.getByText(
      "This original will remain on this public computer after sign-out.",
    ),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Close viewer" }));
  fireEvent.click(screen.getByRole("button", { name: "Select photos" }));
  expect(
    screen.getByText(
      "Subset archive files will remain on this public computer after sign-out.",
    ),
  ).toBeVisible();
});

test("plans complete Event and explicit subset archives with Session CSRF", async () => {
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
              preview_url: "/api/me/media/media-1/preview",
              video_url: "",
              original_url: "/api/me/media/media-1/original",
            },
            {
              id: "media-2",
              media_type: "image",
              width: 900,
              height: 900,
              local_date_time: "2026-07-27T13:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/media-2/thumbnail",
              preview_url: "/api/me/media/media-2/preview",
              video_url: "",
              original_url: "/api/me/media/media-2/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path.startsWith("/api/me/events?")) {
        return json({
          events: [
            {
              id: "event-1",
              publication_id: "publication-1",
              title: "Family weekend",
              description: "",
              committed_at: "2026-07-27T12:00:00Z",
              cover_media_id: "media-1",
              cover_width: 1600,
              cover_height: 900,
              cover_available: true,
              thumbnail_url: "/api/me/media/media-1/thumbnail",
              media_count: 2,
            },
          ],
          next_cursor: null,
        });
      }
      if (path.startsWith("/api/me/events/event-1?")) {
        return json({
          id: "event-1",
          publication_id: "publication-1",
          title: "Family weekend",
          description: "",
          committed_at: "2026-07-27T12:00:00Z",
          cover_media_id: "media-1",
          cover_available: true,
          media_count: 2,
          media: [],
          next_cursor: null,
        });
      }
      if (path === "/api/me/archives") {
        const body = archiveRequest(init);
        return json({
          name: body.scope === "event" ? "Family-weekend" : "Memento-selection",
          item_count: 2,
          total_size: 30,
          expires_at: "2026-07-27T12:15:00Z",
          parts: [
            {
              part_number: 1,
              size: 30,
              filename: "safe-name.zip",
              download_url: "/api/me/archives/parts/1?token=private-token",
            },
          ],
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(await screen.findByRole("button", { name: "Select photos" }));
  fireEvent.click(screen.getByRole("checkbox", { name: /Select Photo 1/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: /Select Photo 2/ }));
  fireEvent.click(screen.getByRole("button", { name: "Download 2 selected" }));
  const subsetLink = await screen.findByRole("link", {
    name: "Download archive",
  });
  expect(subsetLink).toHaveAttribute(
    "href",
    "/api/me/archives/parts/1?token=private-token",
  );
  expect(subsetLink).toHaveAttribute("download", "safe-name.zip");

  const subsetRequest = requests.find(
    ({ path, init }) =>
      path === "/api/me/archives" && archiveRequest(init).scope === "subset",
  );
  expect(subsetRequest?.init).toMatchObject({
    method: "POST",
    headers: { "X-Memento-CSRF": session.csrf_token },
  });
  expect(archiveRequest(subsetRequest?.init)).toEqual({
    scope: "subset",
    event_id: null,
    media_ids: ["media-1", "media-2"],
  });

  fireEvent.click(screen.getAllByRole("button", { name: "Events" })[0]);
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Download Event" }),
  );
  await waitFor(() =>
    expect(
      requests.some(
        ({ path, init }) =>
          path === "/api/me/archives" &&
          archiveRequest(init).scope === "event" &&
          archiveRequest(init).event_id === "event-1",
      ),
    ).toBe(true),
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
  expect(await screen.findByText("No Favorites yet.")).toBeVisible();
});

test("does not claim a library is empty when its request fails", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return apiError("Photos are temporarily unavailable.");
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path.startsWith("/api/me/events?")) {
        return apiError("Events are temporarily unavailable.");
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();

  expect(
    await screen.findByText("Photos are temporarily unavailable."),
  ).toBeVisible();
  expect(
    screen.queryByText("No photos are available."),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getAllByRole("button", { name: "Events" })[0]);
  expect(
    await screen.findByText("Events are temporarily unavailable."),
  ).toBeVisible();
  expect(
    screen.queryByText("No Events are available."),
  ).not.toBeInTheDocument();
});
