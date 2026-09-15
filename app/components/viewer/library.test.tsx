import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";
import type {
  ViewerEntry,
  ViewerLibrary,
} from "../../types/generated/publishing";

const library: ViewerLibrary = {
  photo_count: 502,
  video_count: 1,
  days: [
    {
      date: "2026-07-05",
      photo_count: 501,
      video_count: 1,
      photo_ratios: Array.from({ length: 501 }, () => 1.5),
    },
    {
      date: "2025-06-14",
      photo_count: 1,
      video_count: 0,
      photo_ratios: [1.5],
    },
  ],
};
const newest: ViewerEntry = {
  id: "newest",
  kind: "IMAGE",
  title: "Sunset",
  captured_at: "2026-07-05T20:00:00",
  available: true,
  thumbnail_url: "/media/newest/thumbnail",
  preview_url: "/media/newest/preview",
  download_url: "/media/newest/original",
  playback_url: "",
  width: 1200,
  height: 800,
  chapters: [],
  chapter_status: "",
};
const morning: ViewerEntry = {
  ...newest,
  id: "morning",
  title: "Morning",
  captured_at: "2026-07-05T08:00:00",
};
const oldest: ViewerEntry = {
  ...newest,
  id: "oldest",
  title: "Last summer",
  captured_at: "2025-06-14T09:00:00",
};
const video: ViewerEntry = {
  ...newest,
  id: "video",
  kind: "VIDEO",
  title: "By the lake",
  playback_url: "/media/video/playback",
};

function mockLibrary(handler: (path: string) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: {
            id: "jamie",
            display_name: "Jamie",
            is_curator: false,
            onboarding_completed_at: "2026-01-01T00:00:00Z",
          },
          auth_mode: "fake",
        });
      if (path === "/api/notifications")
        return Response.json({ notifications: [], unread: 0 });
      return handler(path);
    }),
  );
}

function populatedLibrary(path: string) {
  if (path === "/api/library") return Response.json(library);
  if (path === "/api/library/photos?from=2026-07-05")
    return Response.json({ entries: [newest], next_cursor: "next/+" });
  if (path === "/api/library/photos?from=2026-07-05&cursor=next%2F%2B")
    return Response.json({ entries: [morning], next_cursor: "" });
  if (path === "/api/library/photos?to=2026-07-05")
    return Response.json({ entries: [oldest], next_cursor: "" });
  if (path === "/api/library/videos")
    return Response.json({ entries: [video], next_cursor: "" });
  throw new Error(`Unexpected request: ${path}`);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

it("opens Library after Albums in viewer navigation with newest days and entries first", async () => {
  mockLibrary((path) =>
    path === "/api/albums" ? Response.json([]) : populatedLibrary(path),
  );
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums");
  render(<App />);
  const navigation = await screen.findByRole("navigation", {
    name: "Main navigation",
  });
  expect(
    within(navigation)
      .getAllByRole("link")
      .map((link) => link.textContent),
  ).toEqual(["Albums", "Library"]);
  await user.click(within(navigation).getByRole("link", { name: "Library" }));
  await screen.findByRole("link", { name: "Open photo Morning" });
  expect(
    screen.getByText(
      "You can view all of your photos and videos across all your albums.",
    ),
  ).toBeVisible();
  await screen.findByRole("link", { name: "Open photo Last summer" });
  expect(
    screen
      .getAllByRole("link", { name: /^Open photo/ })
      .map((link) => link.getAttribute("aria-label")),
  ).toEqual([
    "Open photo Sunset",
    "Open photo Morning",
    "Open photo Last summer",
  ]);
  expect(
    screen
      .getAllByRole("heading", { level: 2 })
      .map((heading) => heading.textContent),
  ).toEqual([
    "Sunday, July 5, 2026 501 photos",
    "Saturday, June 14, 2025 1 photo",
  ]);
  expect(document.title).toBe("Library | Memento");
  expect(window.location.pathname).toBe("/library/photos");
  expect(
    within(navigation).getByRole("link", { name: "Library" }),
  ).toHaveAttribute("aria-current", "page");
  expect(
    within(navigation).getByRole("link", { name: "Albums" }),
  ).toHaveAttribute("href", "/albums");
  const tabs = screen.getByRole("navigation", { name: "Library media" });
  await user.click(within(tabs).getByRole("link", { name: "Videos 1" }));
  expect(
    await screen.findByRole("link", { name: "Open video By the lake" }),
  ).toHaveAttribute("href", "/library/videos/video");
  expect(
    screen.queryByRole("link", { name: /^Open photo/ }),
  ).not.toBeInTheDocument();
  expect(window.location.pathname).toBe("/library/videos");
  expect(
    within(navigation).getByRole("link", { name: "Library" }),
  ).toHaveAttribute("aria-current", "page");
});

it("reloads a library lightbox beyond the first cursor page and walks newest to oldest", async () => {
  mockLibrary(populatedLibrary);
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/library/photos/morning");
  render(<App />);
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByRole("img", { name: "Morning" });
  expect(
    within(dialog).getByRole("navigation", { name: "Photos filmstrip" }),
  ).toBeVisible();
  await user.keyboard("{ArrowLeft}");
  expect(window.location.pathname).toBe("/library/photos/newest");
  await user.keyboard("{ArrowRight}{ArrowRight}");
  expect(window.location.pathname).toBe("/library/photos/oldest");
  await user.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(window.location.pathname).toBe("/library/photos");
});

it("returns a missing library photo to the photo grid", async () => {
  mockLibrary(populatedLibrary);
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/library/photos/missing");
  render(<App />);
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByRole("heading", { name: "Photo not available" });
  await user.click(
    within(dialog).getByRole("button", { name: "Back to photos" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(window.location.pathname).toBe("/library/photos");
});

it("offers Library after Albums in mobile navigation and closes the menu on selection", async () => {
  mockLibrary((path) =>
    path === "/api/albums" ? Response.json([]) : populatedLibrary(path),
  );
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums");
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Open navigation" }),
  );
  const navigation = screen.getByRole("navigation", {
    name: "Mobile navigation",
  });
  expect(
    within(navigation)
      .getAllByRole("link")
      .map((link) => link.textContent),
  ).toEqual(["Albums", "Library"]);
  await user.click(within(navigation).getByRole("link", { name: "Library" }));
  expect(
    await screen.findByRole("heading", { name: "Your library" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("navigation", { name: "Mobile navigation" }),
  ).not.toBeInTheDocument();
});

it.each([
  {
    tab: "photos",
    photo_count: 0,
    video_count: 1,
    link: "Watch videos",
    to: "/library/videos",
  },
  {
    tab: "videos",
    photo_count: 1,
    video_count: 0,
    link: "View photos",
    to: "/library/photos",
  },
])(
  "offers $link when the other library tab has media",
  async ({ tab, photo_count, video_count, link, to }) => {
    mockLibrary((path) => {
      if (path === "/api/library")
        return Response.json({
          photo_count,
          video_count,
          days: [
            {
              date: "2026-07-05",
              photo_count,
              video_count,
              photo_ratios: photo_count ? [1.5] : [],
            },
          ],
        });
      throw new Error(`Unexpected request: ${path}`);
    });
    window.history.replaceState(null, "", `/library/${tab}`);
    render(<App />);
    expect(await screen.findByRole("link", { name: link })).toHaveAttribute(
      "href",
      to,
    );
  },
);

it("shows a library-specific empty state and retries a failed summary", async () => {
  let failed = true;
  mockLibrary((path) => {
    if (path === "/api/library")
      return failed
        ? Response.json({ message: "Unavailable" }, { status: 500 })
        : Response.json({ photo_count: 0, video_count: 0, days: [] });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/library/photos");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Could not load library" }),
  ).toBeVisible();
  failed = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  expect(
    await screen.findByRole("heading", {
      name: "No photos shared with you yet",
    }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Watch videos" }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByRole("link", { name: "Videos 0" }));
  expect(
    await screen.findByRole("heading", {
      name: "No videos shared with you yet",
    }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "View photos" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText(/in this album/)).not.toBeInTheDocument();
});
