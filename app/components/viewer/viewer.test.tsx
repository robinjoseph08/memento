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
import type {
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";

const album: ViewerAlbum = {
  id: "lake",
  title: "A weekend by the lake",
  description: "Two nights at the cabin.",
  photo_count: 2,
  video_count: 0,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "/media/lake/cover/thumb?person=jamie",
  cover_preview_url: "/media/lake/cover?person=jamie",
  days: [{ date: "2025-06-14", photo_count: 2, video_count: 0 }],
};
const photo: ViewerEntry = {
  id: "photo-1",
  kind: "IMAGE",
  title: "Lake",
  captured_at: "2025-06-14T00:15:00Z",
  available: true,
  thumbnail_url: "/media/lake/photo-1/thumb?person=jamie",
  preview_url: "/media/lake/photo-1?person=jamie",
  download_url: "/media/lake/photo-1/original?person=jamie",
  playback_url: "",
  width: 1200,
  height: 800,
  chapters: [],
  chapter_status: "",
};
const cabin: ViewerEntry = {
  ...photo,
  id: "photo-2",
  title: "Cabin",
  captured_at: "2025-06-14T09:00:00Z",
  preview_url: "/media/lake/photo-2?person=jamie",
  thumbnail_url: "/media/lake/photo-2/thumb?person=jamie",
  download_url: "/media/lake/photo-2/original?person=jamie",
};
const dock: ViewerEntry = {
  ...photo,
  id: "photo-3",
  title: "Dock",
  captured_at: "2025-06-14T18:30:00Z",
  preview_url: "/media/lake/photo-3?person=jamie",
  thumbnail_url: "/media/lake/photo-3/thumb?person=jamie",
  download_url: "/media/lake/photo-3/original?person=jamie",
};
function mockViewer(handler: (path: string) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: { id: "jamie", display_name: "Jamie", is_curator: false },
          auth_mode: "fake",
        });
      return handler(path);
    }),
  );
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("opens the authorized photo route with the shared header and local capture-day counts", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(album);
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: album.title }),
  ).toBeVisible();
  expect(screen.getByText(album.description)).toBeVisible();
  expect(screen.getByRole("img", { name: "Album cover" })).toHaveAttribute(
    "src",
    album.cover_preview_url,
  );
  expect(
    await screen.findByRole("heading", {
      name: "Saturday, June 14, 2025 2 photos",
    }),
  ).toBeVisible();
  expect(screen.getByRole("img", { name: "Lake" })).toHaveAttribute(
    "src",
    photo.preview_url,
  );
  const tabs = screen.getByRole("navigation", { name: "Album media" });
  expect(within(tabs).getByRole("link", { name: "Photos 2" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(within(tabs).getByRole("link", { name: "Videos 0" })).toHaveAttribute(
    "href",
    "/albums/lake/videos",
  );
  expect(
    screen.queryByRole("button", { name: /download|play|open photo/i }),
  ).not.toBeInTheDocument();
  expect(document.title).toBe("A weekend by the lake | Memento");
  expect(window.location.pathname).toBe("/albums/lake/photos");
});

it("lists only the authorized projection with a cover, date range, and separate counts", async () => {
  mockViewer((path) => {
    if (path === "/api/albums") return Response.json([album]);
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums");
  render(<App />);
  const card = await screen.findByRole("link", {
    name: /A weekend by the lake/,
  });
  expect(card).toHaveAttribute("href", "/albums/lake/photos");
  expect(within(card).getByRole("img", { name: album.title })).toHaveAttribute(
    "src",
    album.cover_url,
  );
  expect(card).toHaveTextContent("2 photos, 0 videos");
  expect(card).toHaveTextContent("June 14, 2025");
  expect(document.title).toBe("Albums | Memento");
});

it("paginates photos independently and keeps a truthful zero-count video tab", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(album);
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "next/+" });
    if (path === "/api/albums/lake/photos?cursor=next%2F%2B")
      return Response.json({
        entries: [{ ...photo, id: "photo-2", title: "Cabin" }],
        next_cursor: "",
      });
    if (path === "/api/albums/lake/videos")
      return Response.json({ entries: [], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  // Later pages arrive in the background, with no click.
  expect(await screen.findByRole("img", { name: "Cabin" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Lake" })).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /Load more/ }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByRole("link", { name: "Videos 0" }));
  expect(
    await screen.findByRole("heading", { name: "No videos in this album" }),
  ).toBeVisible();
  expect(screen.queryByRole("img", { name: "Lake" })).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View photos" })).toHaveAttribute(
    "href",
    "/albums/lake/photos",
  );
  expect(window.location.pathname).toBe("/albums/lake/videos");
});

it("lists videos as links to their lightbox without mounting a player in the grid", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({
        ...album,
        photo_count: 0,
        video_count: 1,
        days: [{ date: "2025-06-14", photo_count: 0, video_count: 1 }],
      });
    if (path === "/api/albums/lake/videos")
      return Response.json({
        entries: [{ ...photo, kind: "VIDEO", title: "DSC_0123" }],
        next_cursor: "",
      });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/videos");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "DSC_0123" }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Saturday, June 14, 2025 1 video" }),
  ).toBeVisible();
  expect(
    within(screen.getByRole("list", { name: "Videos" })).queryByText("Video"),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /play|chapters|download/i }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "Open video DSC_0123" }),
  ).toHaveAttribute("href", "/albums/lake/videos/photo-1");
  expect(document.querySelector("video")).toBeNull();
});

it("keeps missing covers neutral and marks failed thumbnails unavailable", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, cover_url: "", cover_preview_url: "" });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByText("No cover")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
  fireEvent.error(await screen.findByRole("img", { name: "Lake" }));
  expect(screen.getByText("Media unavailable")).toBeVisible();
  expect(screen.queryByRole("img", { name: "Lake" })).not.toBeInTheDocument();
});

it("hides previously loaded thumbnails when the server denies further gallery access", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(album);
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "more" });
    if (path === "/api/albums/lake/photos?cursor=more")
      return Response.json({}, { status: 404 });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "no longer available",
  );
  expect(screen.queryByRole("img", { name: "Lake" })).not.toBeInTheDocument();
});

it("preserves loaded photos after a failed next page and retries the cursor rather than replacing them", async () => {
  let retry = false;
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(album);
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "more" });
    if (path === "/api/albums/lake/photos?cursor=more")
      return retry
        ? Response.json({
            entries: [{ ...photo, id: "photo-2", title: "Cabin" }],
            next_cursor: "",
          })
        : Response.json({}, { status: 503 });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not load photos",
  );
  expect(screen.getByRole("img", { name: "Lake" })).toBeVisible();
  retry = true;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(await screen.findByRole("img", { name: "Cabin" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Lake" })).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("opens a routed lightbox from the grid, moves with keys and the filmstrip, and returns focus to the opening photo", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, photo_count: 3 });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo, cabin, dock], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  const opener = await screen.findByRole("link", { name: "Open photo Cabin" });
  expect(opener).toHaveAttribute("href", "/albums/lake/photos/photo-2");
  await user.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Photo 2 of 3" });
  expect(window.location.pathname).toBe("/albums/lake/photos/photo-2");
  expect(within(dialog).getByRole("img", { name: "Cabin" })).toHaveAttribute(
    "src",
    cabin.preview_url,
  );
  expect(within(dialog).getByText(album.title)).toBeVisible();
  expect(within(dialog).getByText("Saturday, June 14, 2025")).toBeVisible();
  expect(within(dialog).queryByText(/9:00/)).not.toBeInTheDocument();
  const download = within(dialog).getByRole("link", {
    name: "Download photo",
  });
  expect(download).toHaveAttribute("href", cabin.download_url);
  expect(download).toHaveAttribute("download");
  expect(dialog).toHaveFocus();
  await user.keyboard("{ArrowRight}");
  expect(
    await screen.findByRole("dialog", { name: "Photo 3 of 3" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/albums/lake/photos/photo-3");
  expect(screen.getByRole("button", { name: "Next photo" })).toBeDisabled();
  await user.keyboard("{ArrowRight}");
  expect(window.location.pathname).toBe("/albums/lake/photos/photo-3");
  const filmstrip = screen.getByRole("navigation", {
    name: "Photos in album",
  });
  expect(
    within(filmstrip).getByRole("button", { name: "Go to photo 3" }),
  ).toHaveAttribute("aria-current", "true");
  await user.click(
    within(filmstrip).getByRole("button", { name: "Go to photo 1" }),
  );
  expect(
    await screen.findByRole("dialog", { name: "Photo 1 of 3" }),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "Previous photo" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Next photo" }));
  expect(
    await screen.findByRole("dialog", { name: "Photo 2 of 3" }),
  ).toBeVisible();
  await user.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(window.location.pathname).toBe("/albums/lake/photos");
  await waitFor(() =>
    expect(
      screen.getByRole("link", { name: "Open photo Cabin" }),
    ).toHaveFocus(),
  );
});

it("reloads a stable photo link beyond the first page once its page arrives", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, photo_count: 3 });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo, cabin], next_cursor: "more" });
    if (path === "/api/albums/lake/photos?cursor=more")
      return Response.json({ entries: [dock], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos/photo-3");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Photo 3 of 3" });
  expect(within(dialog).getByRole("img", { name: "Dock" })).toHaveAttribute(
    "src",
    dock.preview_url,
  );
  expect(document.title).toBe("A weekend by the lake | Memento");
  await user.click(screen.getByRole("button", { name: "Close photo" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(window.location.pathname).toBe("/albums/lake/photos");
  await waitFor(() =>
    expect(screen.getByRole("link", { name: "Open photo Dock" })).toHaveFocus(),
  );
});

it("explains a photo link that is not available without opening another photo", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(album);
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo, cabin], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos/missing");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Photo" });
  expect(
    await within(dialog).findByRole("heading", { name: "Photo not available" }),
  ).toBeVisible();
  expect(
    within(dialog).queryByRole("img", { name: /Lake|Cabin/ }),
  ).not.toBeInTheDocument();
  expect(
    within(dialog).queryByRole("link", { name: "Download photo" }),
  ).not.toBeInTheDocument();
  await user.click(
    within(dialog).getByRole("button", { name: "Back to album" }),
  );
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(window.location.pathname).toBe("/albums/lake/photos");
  expect(screen.getByRole("link", { name: "Open photo Lake" })).toBeVisible();
});

it("keeps the lightbox open when a later page fails and can retry a photo on that page", async () => {
  let retry = false;
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, photo_count: 3 });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo, cabin], next_cursor: "more" });
    if (path === "/api/albums/lake/photos?cursor=more")
      return retry
        ? Response.json({ entries: [dock], next_cursor: "" })
        : Response.json({}, { status: 503 });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos/photo-3");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Photo" });
  expect(
    await within(dialog).findByRole("heading", {
      name: "Could not load photo",
    }),
  ).toBeVisible();
  expect(within(dialog).queryByRole("status")).not.toBeInTheDocument();
  retry = true;
  await user.click(within(dialog).getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("dialog", { name: "Photo 3 of 3" }),
  ).toBeVisible();
  await user.keyboard("{ArrowLeft}");
  expect(
    await screen.findByRole("dialog", { name: "Photo 2 of 3" }),
  ).toBeVisible();
});

it("counts rapid key presses from the last requested photo, not the last rendered one", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, photo_count: 3 });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo, cabin, dock], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/photos/photo-3");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Photo 3 of 3" });
  // Two presses before React has rendered the first one's result.
  act(() => {
    fireEvent.keyDown(dialog, { key: "ArrowLeft" });
    fireEvent.keyDown(dialog, { key: "ArrowLeft" });
  });
  expect(
    await screen.findByRole("dialog", { name: "Photo 1 of 3" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/albums/lake/photos/photo-1");
});

it("keeps chaining pages in the background until the album is complete", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, photo_count: 3 });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "two" });
    if (path === "/api/albums/lake/photos?cursor=two")
      return Response.json({ entries: [cabin], next_cursor: "three" });
    if (path === "/api/albums/lake/photos?cursor=three")
      return Response.json({ entries: [dock], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByRole("img", { name: "Dock" })).toBeVisible();
  expect(screen.getAllByRole("link", { name: /Open photo/ })).toHaveLength(3);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

const videoAlbum: ViewerAlbum = {
  ...album,
  photo_count: 0,
  video_count: 3,
  days: [{ date: "2025-06-14", photo_count: 0, video_count: 3 }],
};
const party: ViewerEntry = {
  ...photo,
  id: "video-1",
  kind: "VIDEO",
  title: "Birthday party",
  captured_at: "2025-06-14T10:00:00Z",
  thumbnail_url: "/media/lake/video-1/thumb?person=jamie",
  preview_url: "/media/lake/video-1?person=jamie",
  download_url: "/media/lake/video-1/original?person=jamie",
  playback_url: "/media/lake/video-1/playback?person=jamie",
  width: 1920,
  height: 1080,
  chapter_status: "complete",
  chapters: [
    { title: "Arrival", start: 0, end: 2 },
    { title: "", start: 2, end: 4 },
    { title: "Goodbyes", start: 4, end: 6 },
  ],
};
const plain: ViewerEntry = {
  ...party,
  id: "video-2",
  title: "DSC_0123",
  captured_at: "2025-06-14T11:00:00Z",
  playback_url: "/media/lake/video-2/playback?person=jamie",
  chapters: [],
};
const broken: ViewerEntry = {
  ...plain,
  id: "video-3",
  title: "DSC_0124",
  captured_at: "2025-06-14T12:00:00Z",
  playback_url: "/media/lake/video-3/playback?person=jamie",
  chapter_status: "failed",
};

// jsdom has no media pipeline. Seeking is a property write and playing is a
// promise, which is all the overlay relies on.
function stubMedia() {
  const times = new WeakMap<HTMLMediaElement, number>();
  const playing = new WeakSet<HTMLMediaElement>();
  Object.defineProperty(HTMLMediaElement.prototype, "currentTime", {
    configurable: true,
    get() {
      return times.get(this as HTMLMediaElement) ?? 0;
    },
    set(value: number) {
      times.set(this as HTMLMediaElement, value);
    },
  });
  Object.defineProperty(HTMLMediaElement.prototype, "paused", {
    configurable: true,
    get() {
      return !playing.has(this as HTMLMediaElement);
    },
  });
  HTMLMediaElement.prototype.play = vi.fn(function (this: HTMLMediaElement) {
    playing.add(this);
    return Promise.resolve();
  });
  HTMLMediaElement.prototype.pause = vi.fn(function (this: HTMLMediaElement) {
    playing.delete(this);
  });
}

it("opens a video in the routed lightbox, seeks by chapter, and shows no picker without chapters", async () => {
  stubMedia();
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(videoAlbum);
    if (path === "/api/albums/lake/videos")
      return Response.json({
        entries: [party, plain, broken],
        next_cursor: "",
      });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/videos");
  render(<App />);
  const opener = await screen.findByRole("link", {
    name: "Open video Birthday party",
  });
  expect(opener).toHaveAttribute("href", "/albums/lake/videos/video-1");
  expect(document.querySelector("video")).toBeNull();
  await user.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Video 1 of 3" });
  expect(window.location.pathname).toBe("/albums/lake/videos/video-1");
  const video =
    within(dialog).getByLabelText<HTMLVideoElement>("Birthday party");
  expect(video.tagName).toBe("VIDEO");
  expect(video).toHaveAttribute("src", party.playback_url);
  expect(video).toHaveAttribute("controls");
  expect(within(dialog).getByText("Birthday party")).toBeVisible();
  expect(video).toHaveClass("h-full", "w-full", "object-contain");
  expect(
    within(dialog).getByRole("link", { name: "Download video" }),
  ).toHaveAttribute("href", party.download_url);
  const filmstrip = within(dialog).getByRole("navigation", {
    name: "Videos in album",
  });
  expect(
    within(filmstrip).getByRole("button", { name: "Go to video 1" }),
  ).toHaveAttribute("aria-current", "true");

  // The chapter picker sits between the title and the date, names the playing
  // chapter, and seeks when another is chosen.
  const picker = within(dialog).getByRole("combobox", { name: "Chapter" });
  expect(picker).toHaveTextContent("Arrival");
  expect(within(dialog).getByText("Saturday, June 14, 2025")).toBeVisible();
  await user.click(picker);
  const options = screen.getAllByRole("option");
  expect(options.map((option) => option.textContent)).toEqual([
    "Arrival0:00",
    "Chapter 20:02",
    "Goodbyes0:04",
  ]);
  // Arrow keys inside the picker's search box edit text; they never step
  // the lightbox to another video underneath the open picker.
  await user.keyboard("ca{ArrowLeft}{ArrowRight}");
  expect(window.location.pathname).toBe("/albums/lake/videos/video-1");
  await user.clear(screen.getByPlaceholderText("Search chapters…"));
  await user.click(screen.getByRole("option", { name: /Goodbyes/ }));
  expect(video.currentTime).toBe(4);
  expect(HTMLMediaElement.prototype.play).toHaveBeenCalled();
  await waitFor(() =>
    expect(screen.queryByRole("option")).not.toBeInTheDocument(),
  );
  fireEvent.timeUpdate(video);
  expect(picker).toHaveTextContent("Goodbyes");
  expect(screen.getByRole("dialog", { name: "Video 1 of 3" })).toBeVisible();

  await user.keyboard("{ArrowRight}");
  expect(
    await screen.findByRole("dialog", { name: "Video 2 of 3" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/albums/lake/videos/video-2");
  // Space plays and pauses without the player itself being focused.
  const next = screen.getByLabelText<HTMLVideoElement>("DSC_0123");
  expect(next.paused).toBe(true);
  await user.keyboard(" ");
  expect(next.paused).toBe(false);
  await user.keyboard(" ");
  expect(next.paused).toBe(true);
  expect(window.location.pathname).toBe("/albums/lake/videos/video-2");
  expect(screen.queryByText(/chapter/i)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("combobox", { name: "Chapter" }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Next video" }));
  expect(
    await screen.findByRole("dialog", { name: "Video 3 of 3" }),
  ).toBeVisible();
  expect(screen.queryByText(/chapter/i)).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Close video" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(window.location.pathname).toBe("/albums/lake/videos");
  expect(document.querySelector("video")).toBeNull();
  await waitFor(() => expect(opener).toHaveFocus());
});

it("reloads a stable video link and hides downloads in preview-style entries", async () => {
  stubMedia();
  mockViewer((path) => {
    if (path === "/api/albums/lake") return Response.json(videoAlbum);
    if (path === "/api/albums/lake/videos")
      return Response.json({
        entries: [party, { ...plain, download_url: "" }],
        next_cursor: "more",
      });
    if (path === "/api/albums/lake/videos?cursor=more")
      return Response.json({ entries: [broken], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/videos/video-2");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Video 2 of 3" });
  expect(within(dialog).getByLabelText("DSC_0123")).toHaveAttribute(
    "src",
    plain.playback_url,
  );
  expect(
    within(dialog).queryByRole("link", { name: "Download video" }),
  ).not.toBeInTheDocument();
  expect(
    await within(dialog).findByRole("button", { name: "Go to video 3" }),
  ).toBeVisible();
});
