import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { ArchiveDownloads, RecipientLibrary } from "./RecipientLibrary";

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

function apiError(message: string, status = 503) {
  return Promise.resolve(
    new Response(JSON.stringify({ error: { message } }), {
      status,
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

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected a JSON body.");
  return body;
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
  vi.useRealTimers();
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
      if (path.startsWith("/api/favorites/")) {
        return json({ media_item_id: path.split("/").at(-1), favorite: false });
      }
      if (path.startsWith("/api/comments/media/")) {
        return json({ comments: [], muted: false });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();

  expect(await screen.findByRole("heading", { name: "Photos" })).toBeVisible();
  const navigation = screen.getAllByRole("navigation", {
    name: "Library navigation",
  });
  expect(navigation).toHaveLength(2);
  for (const destination of ["Photos", "Events", "Favorites"]) {
    expect(screen.getAllByRole("button", { name: destination })).toHaveLength(
      2,
    );
  }
  expect(navigation[0]).not.toHaveTextContent("Search");
  expect(screen.getAllByRole("button", { name: "Search" })).toHaveLength(1);
  expect(screen.getByRole("button", { name: "Search library" })).toBeVisible();
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
      if (path.startsWith("/api/favorites/")) {
        return json({ media_item_id: "media-1", favorite: false });
      }
      if (path.startsWith("/api/comments/media/")) {
        return json({ comments: [], muted: false });
      }
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

test("plans and downloads complete Event and explicit subset archives with Session CSRF", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  const createObjectURL = vi
    .spyOn(URL, "createObjectURL")
    .mockReturnValueOnce("blob:archive-one")
    .mockReturnValueOnce("blob:archive-two");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
  const downloads: Array<{ filename: string; href: string }> = [];
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push({ filename: this.download, href: this.href });
    });
  const expiresAt = new Date(Date.now() + 15 * 60 * 1000).toISOString();
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
          expires_at: expiresAt,
          parts: [
            {
              part_number: 1,
              size: 12,
              filename: "safe-part-one.zip",
              download_url: "/api/me/archives/parts/1?token=token-one",
            },
            {
              part_number: 2,
              size: 18,
              filename: "safe-part-two.zip",
              download_url: "/api/me/archives/parts/2?token=token-two",
            },
          ],
        });
      }
      if (path === "/api/me/archives/parts/1?token=token-one") {
        return Promise.resolve(
          new Response("zip-one", {
            headers: { "Content-Type": "application/zip" },
          }),
        );
      }
      if (path === "/api/me/archives/parts/2?token=token-two") {
        return Promise.resolve(
          new Response("zip-two", {
            headers: { "Content-Type": "application/zip" },
          }),
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(await screen.findByRole("button", { name: "Select photos" }));
  fireEvent.click(screen.getByRole("checkbox", { name: /Select Photo 1/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: /Select Photo 2/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "Prepare archive for 2 selected" }),
  );
  const firstSubsetDownload = await screen.findByRole("button", {
    name: "Download part 1",
  });
  const secondSubsetDownload = screen.getByRole("button", {
    name: "Download part 2",
  });
  expect(screen.getByText("safe-part-one.zip (12 bytes)")).toBeVisible();
  expect(screen.getByText("safe-part-two.zip (18 bytes)")).toBeVisible();

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

  fireEvent.click(firstSubsetDownload);
  await waitFor(() =>
    expect(
      requests.find(
        ({ path }) => path === "/api/me/archives/parts/1?token=token-one",
      )?.init,
    ).toMatchObject({
      method: "POST",
      headers: {
        Accept: "application/zip",
        "X-Memento-CSRF": session.csrf_token,
      },
    }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Downloaded part 1" }),
    ).toBeDisabled(),
  );
  expect(secondSubsetDownload).toBeEnabled();

  fireEvent.click(secondSubsetDownload);
  await waitFor(() =>
    expect(
      requests.find(
        ({ path }) => path === "/api/me/archives/parts/2?token=token-two",
      )?.init,
    ).toMatchObject({
      method: "POST",
      headers: {
        Accept: "application/zip",
        "X-Memento-CSRF": session.csrf_token,
      },
    }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Downloaded part 2" }),
    ).toBeDisabled(),
  );
  expect(
    screen.getByRole("button", { name: "Downloaded part 1" }),
  ).toBeDisabled();
  expect(createObjectURL).toHaveBeenCalledTimes(2);
  expect(createObjectURL).toHaveBeenNthCalledWith(1, expect.any(Blob));
  expect(createObjectURL).toHaveBeenNthCalledWith(2, expect.any(Blob));
  expect(click).toHaveBeenCalledTimes(2);
  expect(downloads).toEqual([
    { filename: "safe-part-one.zip", href: "blob:archive-one" },
    { filename: "safe-part-two.zip", href: "blob:archive-two" },
  ]);

  fireEvent.click(screen.getAllByRole("button", { name: "Events" })[0]);
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Prepare Event archive" }),
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

test("expires archive controls at the server-provided deadline", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-07-27T12:14:00Z"));
  const expiresAt = "2026-07-27T12:15:00Z";

  render(
    <ArchiveDownloads
      csrfToken={session.csrf_token}
      plan={{
        name: "Family-weekend",
        item_count: 2,
        total_size: 30,
        expires_at: expiresAt,
        parts: [
          {
            part_number: 1,
            size: 12,
            filename: "part-one.zip",
            download_url: "/api/me/archives/parts/1?token=one",
          },
          {
            part_number: 2,
            size: 18,
            filename: "part-two.zip",
            download_url: "/api/me/archives/parts/2?token=two",
          },
        ],
      }}
      publicComputer={false}
    />,
  );

  const downloads = screen.getByRole("region", { name: "Archive downloads" });
  expect(downloads).toHaveTextContent("Available until");
  expect(downloads.querySelector("time")).toHaveAttribute(
    "datetime",
    expiresAt,
  );
  expect(screen.getByRole("button", { name: "Download part 1" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Download part 2" })).toBeEnabled();

  act(() => {
    vi.advanceTimersByTime(60 * 1000);
  });

  expect(
    screen.getByText(
      "Archive plan expired. Prepare a new archive to download it.",
    ),
  ).toBeVisible();
  expect(downloads).not.toHaveTextContent("Available until");
  expect(
    screen.getByRole("button", { name: "Download part 1" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Download part 2" }),
  ).toBeDisabled();
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

test("keeps private search text in a POST body and renders safe grouped results", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/me/photos?")) {
        return json({ media: [], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/search") {
        return json({
          events: [
            {
              id: "event-search",
              title: "Café Reunion",
              description: "",
              media_count: 1,
              date_start: "2026-07-27",
              date_end: "2026-07-27",
              cover_media_id: "media-search",
              cover_width: 1200,
              cover_height: 800,
              cover_available: true,
              thumbnail_url: "/api/me/media/media-search/thumbnail",
            },
          ],
          photos: [
            {
              id: "media-search",
              media_type: "image",
              width: 1200,
              height: 800,
              local_date_time: "2026-07-27T10:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/media-search/thumbnail",
              preview_url: "/api/me/media/media-search/preview",
              video_url: "",
              original_url: "/api/me/media/media-search/original",
            },
          ],
          people: [
            {
              person_id: "person-search",
              person_name: "José Alvarez",
              event_id: "event-search",
              event_title: "Café Reunion",
            },
          ],
          total_events: 1,
          total_photos: 1,
          has_more: false,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(screen.getByRole("button", { name: "Search library" }));
  fireEvent.change(
    screen.getByRole("searchbox", {
      name: "Search published Events, Place labels, and People",
    }),
    { target: { value: "José café" } },
  );
  fireEvent.change(screen.getByLabelText("Date filter"), {
    target: { value: "month" },
  });
  fireEvent.change(screen.getByLabelText("Month"), {
    target: { value: "2026-07" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Run search" }));

  expect(await screen.findByText("José Alvarez")).toBeVisible();
  expect(screen.getByText(/attended part of Café Reunion/)).toBeVisible();
  expect(screen.getByText("1 matching photo. 1 matching Event.")).toBeVisible();
  const searchRequest = requests.find(
    (request) => request.path === "/api/search",
  );
  expect(searchRequest?.init?.method).toBe("POST");
  expect(searchRequest?.path).not.toContain("José");
  expect(JSON.parse(searchRequest?.init?.body as string)).toEqual({
    query: "José café",
    date: { kind: "month", month: "2026-07" },
  });

  fireEvent.change(
    screen.getByRole("searchbox", {
      name: "Search published Events, Place labels, and People",
    }),
    { target: { value: "new private search" } },
  );
  expect(screen.queryByText("José Alvarez")).not.toBeInTheDocument();
  expect(
    screen.queryByText("1 matching photo. 1 matching Event."),
  ).not.toBeInTheDocument();
});

test("presents independent photo and Event totals for a range-only Event match", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/me/photos?")) {
        return json({ media: [], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/me/engagement") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/search") {
        return json({
          events: [
            {
              id: "event-range-match",
              title: "Summer holiday",
              description: "",
              media_count: 0,
              date_start: null,
              date_end: null,
              cover_media_id: "authorized-cover",
              cover_width: 1200,
              cover_height: 800,
              cover_available: true,
              thumbnail_url: "/api/me/media/authorized-cover/thumbnail",
            },
          ],
          photos: [],
          people: [],
          total_events: 1,
          total_photos: 0,
          has_more: false,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(screen.getByRole("button", { name: "Search library" }));
  fireEvent.change(screen.getByLabelText("Date filter"), {
    target: { value: "date" },
  });
  fireEvent.change(screen.getByLabelText("Date"), {
    target: { value: "2026-07-25" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Run search" }));

  expect(
    await screen.findByText("0 matching photos. 1 matching Event."),
  ).toBeVisible();
  const result = screen.getByRole("button", { name: /Summer holiday/ });
  expect(result).toHaveTextContent("0 matching items");
  fireEvent.click(result);
  await waitFor(() => {
    const engagement = requests.find(
      (request) =>
        request.path === "/api/me/engagement" &&
        typeof request.init?.body === "string" &&
        request.init.body.includes('"event_opened"'),
    );
    const engagementBody = engagement?.init?.body;
    if (typeof engagementBody !== "string") {
      throw new Error("Expected an Event engagement request");
    }
    const body = JSON.parse(engagementBody) as Record<string, unknown>;
    expect(body).toMatchObject({
      kind: "event_opened",
      event_id: "event-range-match",
      document_visible: true,
    });
  });
  expect(
    screen.queryByText("Nothing in your shared collection matched."),
  ).not.toBeInTheDocument();
});

test("shows safe bounded-term search guidance without echoing the query in the error", async () => {
  const privateQuery =
    "private01 private02 private03 private04 private05 private06 private07 private08 private09 private10 private11 private12 private13";
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return json({ media: [], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/search") {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              error: { message: "Use 12 or fewer unique search terms." },
            }),
            {
              status: 422,
              headers: { "Content-Type": "application/json" },
            },
          ),
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(screen.getByRole("button", { name: "Search library" }));
  fireEvent.change(
    screen.getByRole("searchbox", {
      name: "Search published Events, Place labels, and People",
    }),
    { target: { value: privateQuery } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Run search" }));

  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("Use 12 or fewer unique search terms.");
  expect(alert).not.toHaveTextContent(privateQuery);
  expect(alert).not.toHaveTextContent(
    "Enter search text or choose one complete year, month, date, or date range.",
  );
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

test("paginates Comments chronologically and uses explicit mute eligibility", async () => {
  const cursor = "comments/cursor+1";
  const commentRequests: string[] = [];
  let resolveNextPage: (response: Response) => void = () => undefined;
  const firstComment = {
    id: "comment-older",
    media_item_id: "media-1",
    author_person_id: "blair",
    author_name: "Blair",
    body: "First chronological Comment",
    state: "active",
    version: 1,
    created_at: "2026-07-28T11:00:00Z",
    edited_at: null,
    moderated_at: null,
    moderator_name: null,
    authored_by_me: false,
    can_edit: false,
    can_delete: false,
    can_moderate: false,
  };
  const secondComment = {
    ...firstComment,
    id: "comment-newer",
    author_person_id: "alex",
    author_name: "Alex",
    body: "Second chronological Comment",
    created_at: "2026-07-28T12:00:00Z",
    authored_by_me: true,
    can_edit: true,
    can_delete: true,
  };

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
              local_date_time: "2026-07-28T12:00:00Z",
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
      if (path === "/api/favorites/media-1") {
        return json({ media_item_id: "media-1", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-1?")) {
        commentRequests.push(path);
        const pageCursor = new URL(
          path,
          "http://memento.test",
        ).searchParams.get("cursor");
        if (pageCursor === cursor) {
          return new Promise<Response>((resolve) => {
            resolveNextPage = resolve;
          });
        }
        return json({
          comments: [firstComment],
          can_mute: true,
          muted: false,
          next_cursor: cursor,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );

  expect(await screen.findByText(firstComment.body)).toBeVisible();
  expect(
    screen.getByRole("checkbox", {
      name: "Mute future Comment notifications",
    }),
  ).toBeEnabled();
  const loadMore = screen.getByRole("button", { name: "Load more Comments" });
  expect(loadMore).toBeEnabled();
  fireEvent.click(loadMore);
  expect(
    await screen.findByRole("button", { name: "Loading…" }),
  ).toBeDisabled();
  expect(commentRequests).toHaveLength(2);
  expect(
    new URL(commentRequests[1], "http://memento.test").searchParams.get(
      "cursor",
    ),
  ).toBe(cursor);

  act(() => {
    resolveNextPage(
      new Response(
        JSON.stringify({
          comments: [secondComment],
          can_mute: true,
          muted: false,
          next_cursor: null,
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    );
  });

  expect(await screen.findByText(secondComment.body)).toBeVisible();
  const renderedComments = document.querySelectorAll(".comment-list > li");
  expect(renderedComments).toHaveLength(2);
  expect(renderedComments[0]).toHaveTextContent(firstComment.body);
  expect(renderedComments[1]).toHaveTextContent(secondComment.body);
  expect(screen.getAllByText(firstComment.body)).toHaveLength(1);
  expect(screen.getAllByText(secondComment.body)).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "Load more Comments" }),
  ).not.toBeInTheDocument();
});

test("favorites, comments, and mute controls stay in the private Media viewer", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  let isFavorite = false;
  let muted = false;
  let commentAttempts = 0;
  let comments = [
    {
      id: "comment-1",
      media_item_id: "media-1",
      author_person_id: "alex",
      author_name: "Alex",
      body: "A private memory",
      state: "active",
      version: 1,
      created_at: "2026-07-28T12:00:00Z",
      edited_at: null,
      moderated_at: null,
      moderator_name: null,
      authored_by_me: true,
      can_edit: true,
      can_delete: true,
      can_moderate: false,
    },
  ];
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
              local_date_time: "2026-07-28T12:00:00Z",
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
      if (path === "/api/favorites/media-1") {
        if (init?.method === "PUT") isFavorite = true;
        if (init?.method === "DELETE") isFavorite = false;
        return json({ media_item_id: "media-1", favorite: isFavorite });
      }
      if (path === "/api/comments/media/media-1/mute") {
        const request = JSON.parse(stringBody(init?.body)) as {
          muted: boolean;
        };
        muted = request.muted;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/comments/comment-1") {
        if (init?.method === "PATCH") {
          const request = JSON.parse(stringBody(init.body)) as { body: string };
          comments[0] = {
            ...comments[0],
            body: request.body,
            version: comments[0].version + 1,
          };
          return json(comments[0]);
        }
        if (init?.method === "DELETE") {
          comments = comments.map((comment) =>
            comment.id === "comment-1"
              ? {
                  ...comment,
                  body: "",
                  state: "deleted",
                  version: comment.version + 1,
                  can_edit: false,
                  can_delete: false,
                }
              : comment,
          );
          return Promise.resolve(new Response(null, { status: 204 }));
        }
      }
      if (path.startsWith("/api/comments/media/media-1")) {
        if (init?.method === "POST") {
          commentAttempts += 1;
          if (commentAttempts === 1)
            return Promise.reject(new Error("Connection lost"));
          const request = JSON.parse(stringBody(init.body)) as { body: string };
          comments = [
            ...comments,
            {
              ...comments[0],
              id: "comment-2",
              body: request.body,
              created_at: "2026-07-28T12:01:00Z",
            },
          ];
          return json(comments[1]);
        }
        return json({ comments, can_mute: true, muted, next_cursor: null });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );

  expect(await screen.findByText("A private memory")).toBeVisible();
  expect(
    screen.getByText("Favorites aren't shared with other recipients."),
  ).toBeVisible();

  vi.spyOn(window, "prompt").mockReturnValueOnce("An edited memory");
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  expect(await screen.findByText("An edited memory")).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "Add Favorite" }));
  expect(
    await screen.findByRole("button", { name: "Remove Favorite" }),
  ).toHaveAttribute("aria-pressed", "true");

  fireEvent.change(screen.getByLabelText("Add a Comment"), {
    target: { value: "Another memory" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Post Comment" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Connection lost");
  fireEvent.click(screen.getByRole("button", { name: "Post Comment" }));
  expect(await screen.findByText("Another memory")).toBeVisible();

  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "Mute future Comment notifications",
    }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", {
        name: "Mute future Comment notifications",
      }),
    ).toBeChecked(),
  );

  const confirmDelete = vi
    .spyOn(window, "confirm")
    .mockReturnValueOnce(false)
    .mockReturnValueOnce(true);
  fireEvent.click(screen.getAllByRole("button", { name: "Delete" })[0]);
  expect(confirmDelete).toHaveBeenCalledWith(
    "Delete this Comment? This cannot be undone.",
  );
  expect(
    requests.some(
      ({ path, init }) =>
        path === "/api/comments/comment-1" && init?.method === "DELETE",
    ),
  ).toBe(false);
  expect(screen.getByText("An edited memory")).toBeVisible();

  fireEvent.click(screen.getAllByRole("button", { name: "Delete" })[0]);
  expect(await screen.findByText("Comment deleted.")).toBeVisible();
  expect(screen.queryByText("An edited memory")).not.toBeInTheDocument();
  const renderedComments = document.querySelectorAll(".comment-list > li");
  expect(renderedComments).toHaveLength(2);
  expect(
    within(renderedComments[0] as HTMLElement).getByText("Comment deleted."),
  ).toBeVisible();
  expect(
    within(renderedComments[0] as HTMLElement).queryByRole("button", {
      name: "Edit",
    }),
  ).not.toBeInTheDocument();
  expect(
    within(renderedComments[0] as HTMLElement).queryByRole("button", {
      name: "Delete",
    }),
  ).not.toBeInTheDocument();
  expect(
    within(renderedComments[1] as HTMLElement).getByText("Another memory"),
  ).toBeVisible();

  const editRequest = requests.find(
    ({ path, init }) =>
      path === "/api/comments/comment-1" && init?.method === "PATCH",
  );
  expect(editRequest?.init).toMatchObject({
    method: "PATCH",
    headers: {
      "If-Match": "1",
      "X-Memento-CSRF": session.csrf_token,
    },
  });
  const deleteRequest = requests.find(
    ({ path, init }) =>
      path === "/api/comments/comment-1" && init?.method === "DELETE",
  );
  expect(deleteRequest?.init).toMatchObject({
    method: "DELETE",
    headers: {
      "If-Match": "2",
      "X-Memento-CSRF": session.csrf_token,
    },
  });
  const favoriteRequest = requests.find(
    ({ path, init }) =>
      path === "/api/favorites/media-1" && init?.method === "PUT",
  );
  expect(favoriteRequest?.init).toMatchObject({
    method: "PUT",
    headers: { "X-Memento-CSRF": session.csrf_token },
  });
  const commentRequests = requests.filter(
    ({ path, init }) =>
      path === "/api/comments/media/media-1" && init?.method === "POST",
  );
  expect(commentRequests).toHaveLength(2);
  expect(commentRequests[0]?.init?.method).toBe("POST");
  const firstCommentHeaders = commentRequests[0]?.init?.headers as Record<
    string,
    string
  >;
  const secondCommentHeaders = commentRequests[1]?.init?.headers as Record<
    string,
    string
  >;
  expect(firstCommentHeaders["X-Memento-CSRF"]).toBe(session.csrf_token);
  expect(firstCommentHeaders["Idempotency-Key"]).toMatch(
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  );
  expect(secondCommentHeaders["Idempotency-Key"]).toBe(
    firstCommentHeaders["Idempotency-Key"],
  );
  const muteRequest = requests.find(
    ({ path, init }) =>
      path === "/api/comments/media/media-1/mute" && init?.method === "PUT",
  );
  expect(muteRequest?.init).toMatchObject({
    method: "PUT",
    headers: { "X-Memento-CSRF": session.csrf_token },
  });
});

test("classifies a photo delivery error from its refreshed retained listing", async () => {
  let accessLost = false;
  const comment = {
    id: "comment-1",
    media_item_id: "media-1",
    author_person_id: "alex",
    author_name: "Alex",
    body: "A retained Comment",
    state: "active",
    version: 1,
    created_at: "2026-07-28T12:00:00Z",
    edited_at: null,
    moderated_at: null,
    moderator_name: null,
    authored_by_me: true,
    can_edit: true,
    can_delete: true,
    can_moderate: false,
  };
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
              local_date_time: "2026-07-28T12:00:00Z",
              available: !accessLost,
              thumbnail_url: accessLost
                ? ""
                : "/api/me/media/media-1/thumbnail",
              preview_url: accessLost ? "" : "/api/me/media/media-1/preview",
              video_url: "",
              original_url: accessLost ? "" : "/api/me/media/media-1/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/favorites/media-1") {
        return json({ media_item_id: "media-1", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-1?")) {
        return json({
          comments: [comment],
          can_mute: true,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );
  expect(await screen.findByText(comment.body)).toBeVisible();

  accessLost = true;
  fireEvent.error(screen.getByAltText("Selected photo preview"));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Media's backing is temporarily unavailable.",
  );
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Edit" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Delete" })).toBeEnabled();
  expect(
    screen.getByRole("checkbox", {
      name: "Mute future Comment notifications",
    }),
  ).toBeEnabled();
  expect(screen.getByLabelText("Add a Comment")).toBeEnabled();
  expect(
    screen.queryByAltText("Selected photo preview"),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Return to Library" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Media viewer" }),
    ).not.toBeInTheDocument(),
  );
  expect(await screen.findByText("Source unavailable")).toBeVisible();

  fireEvent.click(
    screen.getByRole("button", { name: "Open Photo 1 from July 2026" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Media's backing is temporarily unavailable.",
  );
  expect(screen.getByText(comment.body)).toBeVisible();
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeEnabled();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Close viewer" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Media viewer" }),
    ).not.toBeInTheDocument(),
  );
});

test("retries a transient representation failure once and then fails closed", async () => {
  let photoCalls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        photoCalls++;
        return json({
          media: [
            {
              id: "media-transient",
              media_type: "image",
              width: 1600,
              height: 900,
              local_date_time: "2026-07-28T12:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/media-transient/thumbnail",
              preview_url: "/api/me/media/media-transient/preview",
              video_url: "",
              original_url: "/api/me/media/media-transient/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/favorites/media-transient") {
        return json({ media_item_id: "media-transient", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-transient?")) {
        return json({
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );
  fireEvent.error(screen.getByAltText("Selected photo preview"));
  await waitFor(() => expect(photoCalls).toBe(2));

  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByAltText("Selected photo preview")).toBeVisible();
  expect(screen.getByRole("link", { name: "Download original" })).toBeVisible();

  fireEvent.error(screen.getByAltText("Selected photo preview"));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Media could not be loaded.",
  );
  expect(photoCalls).toBe(3);
  expect(screen.getByText("Media unavailable")).toBeVisible();
  expect(
    screen.queryByAltText("Selected photo preview"),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeEnabled();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();
});

test("does not classify a failed listing refresh as withdrawn", async () => {
  let photoCalls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        photoCalls++;
        if (photoCalls > 1) {
          return apiError("Photos are temporarily unavailable.");
        }
        return json({
          media: [
            {
              id: "media-unconfirmed",
              media_type: "image",
              width: 1600,
              height: 900,
              local_date_time: "2026-07-28T12:00:00Z",
              available: true,
              thumbnail_url: "/api/me/media/media-unconfirmed/thumbnail",
              preview_url: "/api/me/media/media-unconfirmed/preview",
              video_url: "",
              original_url: "/api/me/media/media-unconfirmed/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/favorites/media-unconfirmed") {
        return json({ media_item_id: "media-unconfirmed", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-unconfirmed?")) {
        return json({
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );
  fireEvent.error(screen.getByAltText("Selected photo preview"));

  expect(
    await screen.findByText(
      "This Media could not be loaded because Library access could not be refreshed. Try again from the Library.",
    ),
  ).toBeVisible();
  expect(
    screen.queryByText("This content is no longer available."),
  ).not.toBeInTheDocument();
  expect(screen.getByText("Access unconfirmed")).toBeVisible();
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeEnabled();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();
  expect(photoCalls).toBe(2);
});

test("classifies a video element delivery error and hides playback controls", async () => {
  let deliveryFailed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return json({
          media: [
            {
              id: "media-video",
              media_type: "video",
              width: 1920,
              height: 1080,
              local_date_time: "2026-07-28T12:00:00Z",
              available: !deliveryFailed,
              thumbnail_url: deliveryFailed
                ? ""
                : "/api/me/media/media-video/thumbnail",
              preview_url: "",
              video_url: deliveryFailed
                ? ""
                : "/api/me/media/media-video/video",
              original_url: deliveryFailed
                ? ""
                : "/api/me/media/media-video/original",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/favorites/media-video") {
        return json({ media_item_id: "media-video", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-video?")) {
        return json({
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Video 1 from July 2026",
    }),
  );
  deliveryFailed = true;
  fireEvent.error(screen.getByLabelText("Video preview"));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Media's backing is temporarily unavailable.",
  );
  expect(screen.queryByLabelText("Video preview")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();
});

test("keeps a withdrawn delivery error privacy-safe without claiming a retained listing", async () => {
  let accessLost = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return json({
          media: accessLost
            ? []
            : [
                {
                  id: "media-1",
                  media_type: "image",
                  width: 1600,
                  height: 900,
                  local_date_time: "2026-07-28T12:00:00Z",
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
      if (path === "/api/favorites/media-1") {
        return json({ media_item_id: "media-1", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-1?")) {
        return json({
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  const opener = await screen.findByRole("button", {
    name: "Open Photo 1 from July 2026",
  });
  fireEvent.click(opener);
  accessLost = true;
  fireEvent.error(screen.getByAltText("Selected photo preview"));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This content is no longer available.",
  );
  expect(
    screen.queryByText(
      /Library listing and interaction history remain available/,
    ),
  ).not.toBeInTheDocument();
});

test("classifies a stale Favorite 404 as withdrawn without a generic mutation error", async () => {
  let withdrawn = false;
  const comment = {
    id: "comment-stale",
    media_item_id: "media-1",
    author_person_id: "alex",
    author_name: "Alex",
    body: "Retained interaction history",
    state: "active",
    version: 1,
    created_at: "2026-07-28T12:00:00Z",
    edited_at: null,
    moderated_at: null,
    moderator_name: null,
    authored_by_me: true,
    can_edit: true,
    can_delete: true,
    can_moderate: false,
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return json({
          media: withdrawn
            ? []
            : [
                {
                  id: "media-1",
                  media_type: "image",
                  width: 1600,
                  height: 900,
                  local_date_time: "2026-07-28T12:00:00Z",
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
      if (path === "/api/favorites/media-1") {
        if (init?.method === "PUT") {
          return apiError("Favorite mutation failed", 404);
        }
        return json({ media_item_id: "media-1", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-1?")) {
        return json({
          comments: [comment],
          can_mute: true,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );
  expect(await screen.findByText(comment.body)).toBeVisible();
  const favorite = screen.getByRole("button", { name: "Add Favorite" });
  expect(favorite).toBeEnabled();

  withdrawn = true;
  fireEvent.click(favorite);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This content is no longer available.",
  );
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Edit" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  expect(
    screen.getByRole("checkbox", {
      name: "Mute future Comment notifications",
    }),
  ).toBeDisabled();
  expect(screen.getByLabelText("Add a Comment")).toBeDisabled();
  expect(screen.getByRole("button", { name: "Post Comment" })).toBeDisabled();
  expect(
    screen.queryByText("Favorite mutation failed"),
  ).not.toBeInTheDocument();
  expect(screen.getAllByRole("alert")).toHaveLength(1);
});

test("refreshes Search after a representation error and retains unavailable Media", async () => {
  let searchCalls = 0;
  const searchMedia = (available: boolean) => ({
    id: "media-search",
    media_type: "image",
    width: 1200,
    height: 800,
    local_date_time: "2026-07-27T10:00:00Z",
    available,
    thumbnail_url: available ? "/api/me/media/media-search/thumbnail" : "",
    preview_url: available ? "/api/me/media/media-search/preview" : "",
    video_url: "",
    original_url: available ? "/api/me/media/media-search/original" : "",
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/me/photos?")) {
        return json({ media: [], next_cursor: null });
      }
      if (path === "/api/me/new-for-you") return json({ events: [] });
      if (path === "/api/search") {
        searchCalls++;
        return json({
          events: [],
          photos: [searchMedia(searchCalls === 1)],
          people: [],
          total_events: 0,
          total_photos: 1,
          has_more: false,
        });
      }
      if (path === "/api/favorites/media-search") {
        return json({ media_item_id: "media-search", favorite: false });
      }
      if (path.startsWith("/api/comments/media/media-search?")) {
        return json({
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderLibrary();
  fireEvent.click(screen.getByRole("button", { name: "Search library" }));
  fireEvent.change(
    screen.getByRole("searchbox", {
      name: "Search published Events, Place labels, and People",
    }),
    { target: { value: "picnic" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Run search" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Open Photo 1 from July 2026",
    }),
  );
  fireEvent.error(screen.getByAltText("Selected photo preview"));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This Media's backing is temporarily unavailable.",
  );
  expect(searchCalls).toBe(2);
  expect(screen.getByRole("button", { name: "Add Favorite" })).toBeEnabled();
  expect(
    screen.queryByRole("link", { name: "Download original" }),
  ).not.toBeInTheDocument();
});
