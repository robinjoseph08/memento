import {
  cleanup,
  fireEvent,
  render,
  screen,
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
  cover_url: "/media/lake/cover?person=jamie",
  days: [{ date: "2025-06-14", photo_count: 2, video_count: 0 }],
};
const photo: ViewerEntry = {
  id: "photo-1",
  kind: "IMAGE",
  title: "Lake",
  captured_at: "2025-06-14T00:15:00Z",
  available: true,
  thumbnail_url: "/media/lake/photo-1?person=jamie",
  width: 1200,
  height: 800,
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
    album.cover_url,
  );
  expect(
    await screen.findByRole("heading", {
      name: "Saturday, June 14, 2025 2 photos",
    }),
  ).toBeVisible();
  expect(screen.getByRole("img", { name: "Lake" })).toHaveAttribute(
    "src",
    photo.thumbnail_url,
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
  await user.click(
    await screen.findByRole("button", { name: "Load more photos" }),
  );
  expect(await screen.findByRole("img", { name: "Cabin" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Lake" })).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Load more photos" }),
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

it("shows video titles and neutral indicators without unfinished playback or chapter controls", async () => {
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
    within(screen.getByRole("list", { name: "Videos" })).getByText("Video"),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /play|chapters|download/i }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /open video|download/i }),
  ).not.toBeInTheDocument();
});

it("keeps missing covers neutral and marks failed thumbnails unavailable", async () => {
  mockViewer((path) => {
    if (path === "/api/albums/lake")
      return Response.json({ ...album, cover_url: "" });
    if (path === "/api/albums/lake/photos")
      return Response.json({ entries: [photo], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByText("No cover available")).toBeVisible();
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
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  expect(await screen.findByRole("img", { name: "Lake" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Load more photos" }));
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
  await user.click(
    await screen.findByRole("button", { name: "Load more photos" }),
  );
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
