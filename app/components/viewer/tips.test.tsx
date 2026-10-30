import {
  cleanup,
  configure,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";
import type { Tip } from "../../types/generated/identity";
import type {
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";

// A Tip arrives a moment after its page, so waits allow for that delay on a
// busy machine.
configure({ asyncUtilTimeout: 3000 });
const arrival = () => new Promise((resolve) => setTimeout(resolve, 1000));

const album: ViewerAlbum = {
  id: "lake",
  title: "A weekend by the lake",
  description: "",
  photo_count: 1,
  video_count: 1,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "",
  cover_preview_url: "",
  has_own_media: false,
  more_photo_count: 0,
  more_video_count: 0,
  joined: false,
  days: [
    {
      date: "2025-06-14",
      photo_count: 1,
      video_count: 1,
      photo_ratios: [1.5],
      video_ratios: [1.5],
    },
  ],
};
const photo: ViewerEntry = {
  id: "photo-1",
  kind: "IMAGE",
  title: "",
  captured_at: "2025-06-14T09:00:00Z",
  available: true,
  thumbnail_url: "/media/lake/photo-1/thumb",
  preview_url: "/media/lake/photo-1",
  download_url: "",
  playback_url: "",
  width: 1200,
  height: 800,
  chapters: [],
  chapter_status: "",
};
const party: ViewerEntry = {
  ...photo,
  id: "video-1",
  kind: "VIDEO",
  title: "Birthday party",
  playback_url: "/media/lake/video-1/playback",
  chapter_status: "complete",
  chapters: [
    { title: "Arrival", start: 0, end: 2 },
    { title: "Goodbyes", start: 2, end: 4 },
  ],
};

// mockTips serves a viewer who has retired the given Tips and records each
// Tip they retire.
function mockTips(
  retired: Tip[],
  handler: (path: string) => Response,
  curator = false,
  methods = ["fake"],
) {
  const retiring: string[] = [];
  const person = () => ({
    id: "jamie",
    display_name: "Jamie",
    is_curator: curator,
    retired_tips: retired,
    onboarding_completed_at: "2026-01-01T00:00:00Z",
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: person(),
          sign_in_methods: methods,
        });
      const tip = /^\/api\/identity\/tips\/(\w+)\/retire$/.exec(path)?.[1];
      if (tip) {
        retiring.push(tip);
        retired = [...new Set([...retired, tip as Tip])];
        return Response.json(person());
      }
      if (path === "/api/identity/tips/reset") {
        retiring.push("reset");
        retired = [];
        return Response.json(person());
      }
      return handler(path);
    }),
  );
  return retiring;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

function lake(path: string) {
  if (path === "/api/albums/lake") return Response.json(album);
  if (path === "/api/albums/lake/photos")
    return Response.json({ entries: [photo], next_cursor: "" });
  if (path === "/api/albums/lake/videos")
    return Response.json({ entries: [party], next_cursor: "" });
  throw new Error(`Unexpected request: ${path}`);
}

it("points a viewer at the Videos tab once and retires it when they are done", async () => {
  const retiring = mockTips(["join", "chapters"], lake);
  window.history.replaceState(null, "", "/albums/lake/photos");
  const user = userEvent.setup();
  render(<App />);
  // The page arrives undimmed, and the Tip follows a moment later.
  await screen.findByRole("link", { name: /^Videos/ });
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  const tip = await screen.findByRole("dialog", {
    name: "Videos have their own tab",
  });
  expect(tip).toHaveTextContent(
    "Videos are kept separate so that you can watch all of them together instead of mixed in with the photos.",
  );
  expect(within(tip).getByRole("button", { name: "Got it" })).toHaveFocus();
  await user.click(within(tip).getByRole("button", { name: "Got it" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Videos have their own tab" }),
    ).not.toBeInTheDocument(),
  );
  expect(retiring).toEqual(["videos"]);
});

it("retires the Videos Tip without showing it when the viewer opens Videos", async () => {
  const retiring = mockTips(["join", "chapters"], lake);
  window.history.replaceState(null, "", "/albums/lake/videos");
  render(<App />);
  await screen.findByRole("link", { name: "Open video Birthday party" });
  await waitFor(() => expect(retiring).toEqual(["videos"]));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("shows Curators no Tips", async () => {
  const retiring = mockTips([], lake, true);
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  await screen.findByRole("heading", { name: album.title });
  await arrival();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(retiring).toEqual([]);
});

it("retires the Join Tip once the viewer looks through an offered Album", async () => {
  const retiring = mockTips(["videos", "chapters"], (path) => {
    if (path === "/api/albums/lake/preview") return Response.json(album);
    if (path === "/api/albums/lake/preview/photos")
      return Response.json({ entries: [photo], next_cursor: "" });
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums/lake/preview/photos");
  render(<App />);
  await screen.findByRole("heading", { name: album.title });
  await waitFor(() => expect(retiring).toEqual(["join"]));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("explains joining at More albums and retires it on Escape", async () => {
  const reunion = { ...album, id: "reunion", title: "Family reunion" };
  const retiring = mockTips(["videos", "chapters"], (path) => {
    if (path === "/api/albums") return Response.json([album]);
    if (path === "/api/albums/more") return Response.json([reunion]);
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  const tip = await screen.findByRole("dialog", {
    name: "Albums you can join",
  });
  expect(tip).toHaveTextContent(
    "You're not in these albums, but you can poke around in them if you're interested.",
  );
  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(retiring).toEqual(["join"]);
});

it("holds a video with chapters still until the Chapters Tip is gone", async () => {
  const play = vi
    .spyOn(HTMLMediaElement.prototype, "play")
    .mockResolvedValue(undefined);
  const retiring = mockTips(["videos", "join"], lake);
  window.history.replaceState(null, "", "/albums/lake/videos/video-1");
  const user = userEvent.setup();
  render(<App />);
  const tip = await screen.findByRole("dialog", {
    name: "This video has chapters",
  });
  expect(tip).toHaveTextContent(
    "Use them to jump around to the parts of the video that you care most about.",
  );
  const video = screen.getByLabelText<HTMLVideoElement>("Birthday party");
  expect(video.autoplay).toBe(false);
  expect(play).not.toHaveBeenCalled();
  await user.click(within(tip).getByRole("button", { name: "Got it" }));
  await waitFor(() => expect(video.autoplay).toBe(true));
  expect(play).toHaveBeenCalled();
  expect(retiring).toEqual(["chapters"]);
  // The lightbox stays open; only the Tip went away.
  expect(
    screen.getByRole("dialog", { name: "Video 1 of 1" }),
  ).toBeInTheDocument();
});

it("keeps a Tip up when focus moves elsewhere on its own", async () => {
  const retiring = mockTips(["join", "chapters"], lake);
  window.history.replaceState(null, "", "/albums/lake/photos");
  render(<App />);
  const tip = await screen.findByRole("dialog", {
    name: "Videos have their own tab",
  });
  screen.getByRole("link", { name: "All albums" }).focus();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(tip).toBeInTheDocument();
  expect(retiring).toEqual([]);
});

it("brings every Tip back from the account menu in development", async () => {
  const retiring = mockTips(["videos", "join", "chapters"], lake);
  window.history.replaceState(null, "", "/albums/lake/photos");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("link", { name: /^Videos/ });
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Account menu" }));
  await user.click(screen.getByRole("menuitem", { name: "Show tips again" }));
  expect(
    await screen.findByRole("dialog", { name: "Videos have their own tab" }),
  ).toBeInTheDocument();
  expect(retiring).toEqual(["reset"]);
});

it("offers no Tip reset outside development", async () => {
  mockTips(["videos", "join", "chapters"], lake, false, ["code"]);
  window.history.replaceState(null, "", "/albums/lake/photos");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("link", { name: /^Videos/ });
  await user.click(screen.getByRole("button", { name: "Account menu" }));
  expect(
    await screen.findByRole("menuitem", { name: /Sign out/ }),
  ).toBeVisible();
  expect(
    screen.queryByRole("menuitem", { name: "Show tips again" }),
  ).not.toBeInTheDocument();
});
