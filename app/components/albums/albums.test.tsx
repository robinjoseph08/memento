import { focusManager } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";
import type { Album, AlbumDetail } from "../../types/generated/publishing";

const album: Album = {
  id: "album-1",
  source_id: "summer",
  title: "Summer by the sea",
  description: "A week away",
  published: false,
  status: "complete",
  message: "",
  processed: 36,
  total: 36,
};

const person = { id: "robin", display_name: "Robin", is_curator: true };
const source = {
  id: "summer",
  title: "Summer by the sea",
  description: "A week away",
  count: 36,
  start_date: "2026-07-01T12:00:00Z",
  end_date: "2026-07-07T12:00:00Z",
  cover_url: "/api/curator/sources/summer/cover",
  album_id: "",
};

function mockAPI(
  handler: (
    path: string,
    options?: RequestInit,
  ) => Response | Promise<Response>,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Promise.resolve(
          Response.json({ claimed: true, person, auth_mode: "fake" }),
        );
      if (path.endsWith("/connection"))
        return Promise.resolve(
          Response.json({
            usable: true,
            import_supported: true,
            version: "2.7.5",
            message: "",
          }),
        );
      return Promise.resolve(handler(path, options));
    }),
  );
}

it("opens the library from an empty collection and links previously imported sources", async () => {
  mockAPI((path) =>
    path.endsWith("/albums")
      ? Response.json([])
      : Response.json({
          albums: [{ ...source, album_id: album.id }],
          page: 1,
          pages: 1,
          total: 1,
        }),
  );
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  await user.click(screen.getByRole("link", { name: "Import an album" }));
  expect(
    await screen.findByRole("link", { name: "Open album" }),
  ).toHaveAttribute("href", "/curator/albums/album-1");
  expect(
    screen.queryByRole("button", { name: "Import" }),
  ).not.toBeInTheDocument();
});

it("imports an album, shows progress, then reveals unpublished Moments with compact media", async () => {
  let imported = false;
  let completed = false;
  mockAPI((path, options) => {
    if (path.includes("/sources?"))
      return Response.json({ albums: [source], page: 1, pages: 1, total: 1 });
    if (path.endsWith("/imports")) {
      expect(JSON.parse(String(options?.body))).toEqual({
        source_id: "summer",
      });
      imported = true;
      return Response.json({
        ...album,
        status: "queued",
        processed: 0,
        moments: [],
      });
    }
    return Response.json(
      completed
        ? completeAlbum
        : { ...album, status: "processing", processed: 12, moments: [] },
    );
  });
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Import" }));
  expect(imported).toBe(true);
  await waitFor(() =>
    expect(
      screen.getByRole("progressbar", { name: "Import progress" }),
    ).toHaveAttribute("value", "12"),
  );
  expect(
    screen.queryByRole("textbox", { name: "Album title" }),
  ).not.toBeInTheDocument();
  completed = true;
  expect(
    await screen.findByRole("heading", { name: "Moments" }, { timeout: 4000 }),
  ).toBeVisible();
  expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  expect(screen.getByText("Unpublished")).toBeVisible();
  const moment = screen.getByRole("region", { name: "First day" });
  expect(
    within(moment).getByRole("list", { name: "Moment media" }),
  ).toBeVisible();
  expect(within(moment).getByText("1 photo, 1 video")).toBeVisible();
  expect(screen.getByRole("img", { name: "Beach.jpg" })).toHaveAttribute(
    "src",
    "/media/beach",
  );
  expect(document.title).toBe("Summer by the sea | Memento");
});

const completeAlbum: AlbumDetail = {
  ...album,
  moments: [
    {
      id: "day-1",
      label: "First day",
      date: "2026-07-01",
      cover_entry_id: "photo",
      entries: [
        {
          id: "photo",
          media_id: "m-photo",
          filename: "Beach.jpg",
          kind: "IMAGE",
          captured_at: "2026-07-01T12:00:00",
          available: true,
          thumbnail_url: "/media/beach",
        },
        {
          id: "video",
          media_id: "m-video",
          filename: "Waves.mp4",
          kind: "VIDEO",
          captured_at: "2026-07-01T13:00:00",
          available: true,
          thumbnail_url: "/media/waves",
        },
      ],
    },
  ],
};

it("presents distinct Album details and expandable Moments without discarding title edits", async () => {
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const navigation = await screen.findByRole("navigation", {
    name: "Album sections",
  });
  expect(
    within(navigation).getByRole("link", { name: /Moments/ }),
  ).toHaveAttribute("aria-current", "page");
  expect(screen.getByRole("heading", { name: "Moments" })).toBeVisible();
  const moment = screen.getByRole("button", { name: "First day" });
  expect(moment).toHaveAttribute("aria-expanded", "true");
  await user.click(moment);
  expect(moment).toHaveAttribute("aria-expanded", "false");
  expect(
    screen.queryByRole("img", { name: "Beach.jpg" }),
  ).not.toBeInTheDocument();
  await user.click(moment);
  expect(screen.getByRole("img", { name: "Beach.jpg" })).toBeVisible();
  await user.click(
    within(navigation).getByRole("link", { name: "Album details" }),
  );
  const title = screen.getByRole("textbox", { name: "Album title" });
  await user.clear(title);
  await user.type(title, "Unsaved weekend");
  await user.click(within(navigation).getByRole("link", { name: /Moments/ }));
  expect(
    screen.queryByRole("textbox", { name: "Album title" }),
  ).not.toBeInTheDocument();
  await user.click(
    within(navigation).getByRole("link", { name: "Album details" }),
  );
  expect(screen.getByRole("textbox", { name: "Album title" })).toHaveValue(
    "Unsaved weekend",
  );
});

it("disables unsupported imports without hiding the library or blocking imported albums", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/connection"))
        return Response.json({
          usable: true,
          import_supported: false,
          version: "2.6.0",
          message: "Imports require Immich 2.7.5 or later.",
        });
      if (path.endsWith("/albums/album-1")) return Response.json(completeAlbum);
      return Response.json({
        albums: [
          source,
          {
            ...source,
            id: "old",
            title: "Previously imported",
            album_id: album.id,
          },
        ],
        page: 1,
        pages: 1,
        total: 2,
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Imports require Immich 2.7.5 or later.",
  );
  expect(
    await screen.findByRole("link", { name: "Open album" }),
  ).toHaveAttribute("href", "/curator/albums/album-1");
  expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  expect(screen.getByRole("heading", { name: source.title })).toBeVisible();
  await user.click(screen.getByRole("link", { name: "Open album" }));
  await user.click(await screen.findByRole("link", { name: "Album details" }));
  expect(
    await screen.findByRole("textbox", { name: "Album title" }),
  ).toHaveValue(album.title);
});

it("keeps title edits through failed refresh, focuses field errors, and saves only the owned title", async () => {
  let current = completeAlbum;
  let failRead = false;
  let failSave = true;
  mockAPI((path, options) => {
    if (options?.method === "POST") {
      if (failSave)
        return Response.json(
          { error: { fields: { title: "Enter an album title." } } },
          { status: 400 },
        );
      expect(JSON.parse(String(options.body))).toEqual({ title: "Our summer" });
      current = { ...current, title: "Our summer" };
    } else if (failRead) return Response.json({}, { status: 503 });
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("link", { name: "Album details" }));
  const title = await screen.findByRole("textbox", { name: "Album title" });
  await user.clear(title);
  await user.type(title, "Our summer");
  expect(screen.getByText("A week away")).toBeVisible();
  expect(screen.getAllByRole("textbox")).toHaveLength(1);
  failRead = true;
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  expect(title).toHaveValue("Our summer");
  failRead = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  await user.click(screen.getByRole("button", { name: "Save title" }));
  expect(await screen.findByText("Enter an album title.")).toBeVisible();
  expect(title).toHaveFocus();
  failSave = false;
  await user.click(screen.getByRole("button", { name: "Save title" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Title saved.");
  expect(screen.getByRole("heading", { name: "Our summer" })).toBeVisible();
  expect(document.title).toBe("Our summer | Memento");
});

it.each(["complete", "failed"])(
  "stops polling when import is %s",
  async (terminal) => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let current = {
      ...completeAlbum,
      status: "processing",
      processed: 12,
      moments: [],
    };
    let reads = 0;
    mockAPI(() => {
      reads++;
      return Response.json(current);
    });
    window.history.replaceState(null, "", "/curator/albums/album-1");
    render(<App />);
    expect(await screen.findByRole("progressbar")).toHaveAttribute(
      "value",
      "12",
    );
    current = { ...current, status: terminal, processed: 36 };
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() =>
      expect(screen.queryByRole("progressbar")).not.toBeInTheDocument(),
    );
    expect(
      screen.getByRole(terminal === "complete" ? "heading" : "button", {
        name: terminal === "complete" ? "Moments" : "Retry import",
      }),
    ).toBeVisible();
    const stoppedAt = reads;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(reads).toBe(stoppedAt);
  },
);

it("stops polling when leaving a running import", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let reads = 0;
  mockAPI((path) => {
    if (path.endsWith("/albums")) return Response.json([]);
    reads++;
    return Response.json({ ...album, status: "processing", moments: [] });
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("progressbar");
  await user.click(screen.getByRole("link", { name: "All albums" }));
  await screen.findByRole("heading", { name: "Your albums" });
  const stoppedAt = reads;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(reads).toBe(stoppedAt);
});

it("retries an interrupted import and keeps showing recovery status until it resumes", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let current = { ...album, status: "interrupted", moments: [] };
  mockAPI((path, options) => {
    if (options?.method === "POST") {
      expect(path).toBe("/api/curator/albums/album-1/retry");
      current = { ...current, status: "queued" };
    }
    return Response.json(current);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Import interrupted" }),
  ).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Retry import" }));
  expect(
    await screen.findByRole("heading", { name: "Waiting to import" }),
  ).toBeVisible();
  current = { ...current, status: "processing" };
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(
    await screen.findByRole("heading", { name: "Importing your album" }),
  ).toBeVisible();
});

it("recovers from a source read failure and explains an empty search", async () => {
  let failed = true;
  mockAPI(() =>
    failed
      ? Response.json({}, { status: 503 })
      : Response.json({ albums: [], page: 1, pages: 0, total: 0 }),
  );
  window.history.replaceState(null, "", "/curator/import?q=absent&page=1");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  failed = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  expect(
    screen.getByRole("searchbox", { name: "Search Immich albums" }),
  ).toHaveValue("absent");
});

it("shows a failed import honestly in the album list and lets the Curator reopen it", async () => {
  mockAPI(() => Response.json([{ ...album, status: "failed" }]));
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  const link = await screen.findByRole("link", { name: /Summer by the sea/ });
  expect(link).toHaveTextContent("Import failed");
  expect(link).toHaveAttribute("href", "/curator/albums/album-1");
});

it("pages back from the server-clamped page instead of an out-of-range bookmark", async () => {
  mockAPI((path) => {
    const requested = Number(
      new URL(path, window.location.origin).searchParams.get("page"),
    );
    return Response.json({
      albums: [source],
      page: Math.min(requested, 3),
      pages: 3,
      total: 60,
    });
  });
  window.history.replaceState(null, "", "/curator/import?q=Summer&page=999");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByText("Page 3 of 3")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Next page" }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByRole("link", { name: "Previous page" }));
  expect(await screen.findByText("Page 2 of 3")).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("page")).toBe("2");
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
});

it("replaces a broken source cover and tries a refreshed cover URL", async () => {
  let cover = source.cover_url;
  mockAPI(() =>
    Response.json({
      albums: [{ ...source, cover_url: cover }],
      page: 1,
      pages: 1,
      total: 1,
    }),
  );
  window.history.replaceState(null, "", "/curator/import");
  render(<App />);
  fireEvent.error(await screen.findByRole("img", { name: source.title }));
  expect(screen.getByText("No cover available")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: source.title }),
  ).not.toBeInTheDocument();
  cover = "/api/curator/sources/summer/cover?v=2";
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(
    await screen.findByRole("img", { name: source.title }),
  ).toHaveAttribute("src", cover);
  expect(screen.queryByText("No cover available")).not.toBeInTheDocument();
});

it("replaces failed imported thumbnails while keeping filenames and other previews", async () => {
  mockAPI(() => Response.json(completeAlbum));
  window.history.replaceState(null, "", "/curator/albums/album-1");
  render(<App />);
  fireEvent.error(await screen.findByRole("img", { name: "Beach.jpg" }));
  expect(screen.getByText("No preview available")).toBeVisible();
  expect(screen.getByText("Beach.jpg")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Beach.jpg" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("img", { name: "Waves.mp4" })).toBeVisible();
});

it.each(["complete", "failed"])(
  "refreshes active album-list statuses and stops when all imports finish with %s",
  async (terminal) => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let status = "queued";
    let reads = 0;
    mockAPI(() => {
      reads++;
      return Response.json([
        { ...album, id: "finished", title: "Already imported" },
        { ...album, status },
      ]);
    });
    window.history.replaceState(null, "", "/curator");
    render(<App />);
    const link = await screen.findByRole("link", { name: /Summer by the sea/ });
    expect(link).toHaveTextContent("Waiting to import");
    expect(reads).toBe(1);
    status = "processing";
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() => expect(link).toHaveTextContent("Import in progress"));
    expect(reads).toBe(2);
    status = "interrupted";
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() => expect(link).toHaveTextContent("Import interrupted"));
    expect(reads).toBe(3);
    status = terminal;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() =>
      expect(link).toHaveTextContent(
        terminal === "complete" ? "36 items" : "Import failed",
      ),
    );
    expect(reads).toBe(4);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(reads).toBe(4);
  },
);

it("stops album-list polling when navigating away from active imports", async () => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  let reads = 0;
  mockAPI((path) => {
    if (path === "/api/curator/albums") {
      reads++;
      return Response.json([{ ...album, status: "processing" }]);
    }
    return Response.json({ albums: [], page: 1, pages: 0, total: 0 });
  });
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("link", { name: /Summer by the sea/ });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(reads).toBe(2);
  await user.click(screen.getByRole("link", { name: "Import an album" }));
  await screen.findByRole("heading", { name: "Import an album" });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(reads).toBe(2);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  focusManager.setFocused(undefined);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

it("browses source covers, searches in the URL and keeps search when paging", async () => {
  mockAPI((path) => {
    const url = new URL(path, window.location.origin);
    return Response.json({
      albums: [
        {
          ...source,
          title:
            url.searchParams.get("page") === "2"
              ? "Summer at home"
              : source.title,
        },
      ],
      page: Number(url.searchParams.get("page") || 1),
      pages: 2,
      total: 25,
    });
  });
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: source.title }),
  ).toBeVisible();
  expect(screen.getByRole("img", { name: source.title })).toHaveAttribute(
    "src",
    source.cover_url,
  );
  expect(screen.getByText("36 items")).toBeVisible();
  expect(screen.getByText(/Jul 1, 2026.*Jul 7, 2026/)).toBeVisible();
  await user.type(
    screen.getByRole("searchbox", { name: "Search Immich albums" }),
    "Summer{Enter}",
  );
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
  await user.click(await screen.findByRole("link", { name: "Next page" }));
  expect(
    await screen.findByRole("heading", { name: "Summer at home" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("q")).toBe("Summer");
  expect(new URLSearchParams(window.location.search).get("page")).toBe("2");
});
