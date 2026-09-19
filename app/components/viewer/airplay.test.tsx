import {
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
  description: "",
  photo_count: 0,
  video_count: 1,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "",
  cover_preview_url: "",
  days: [
    {
      date: "2025-06-14",
      photo_count: 0,
      video_count: 1,
      photo_ratios: [],
      video_ratios: [1.78],
    },
  ],
};
const party: ViewerEntry = {
  id: "video-1",
  kind: "VIDEO",
  title: "Birthday party",
  captured_at: "2025-06-14T10:00:00Z",
  available: true,
  thumbnail_url: "/media/lake/video-1/thumb?person=jamie",
  preview_url: "/media/lake/video-1?person=jamie",
  download_url: "/media/lake/video-1/original?person=jamie",
  playback_url: "/media/lake/video-1/playback?person=jamie",
  width: 1920,
  height: 1080,
  chapter_status: "complete",
  chapters: [
    { title: "Arrival", start: 0, end: 2 },
    { title: "Goodbyes", start: 4, end: 6 },
  ],
};
const signedURL = "https://memento.example/api/media/signed/playback/token?v=1";

type AirPlayVideo = HTMLVideoElement & {
  webkitShowPlaybackTargetPicker: () => void;
  webkitCurrentPlaybackTargetIsWireless: boolean;
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

type Mint = { entry_id: string; variant: string };

// Opens the video lightbox in a browser that can AirPlay. jsdom has no media
// pipeline: position is a property and playing a promise.
async function openVideo(refuse: boolean) {
  vi.stubGlobal("WebKitPlaybackTargetAvailabilityEvent", class {});
  // jsdom has no Media Session; browsers name what is playing through it.
  vi.stubGlobal(
    "MediaMetadata",
    class {
      title: string;
      constructor(init: { title: string }) {
        this.title = init.title;
      }
    },
  );
  Object.defineProperty(navigator, "mediaSession", {
    configurable: true,
    value: { metadata: null },
  });
  const times = new WeakMap<HTMLMediaElement, number>();
  Object.defineProperty(HTMLMediaElement.prototype, "currentTime", {
    configurable: true,
    get() {
      return times.get(this as HTMLMediaElement) ?? 0;
    },
    set(value: number) {
      times.set(this as HTMLMediaElement, value);
    },
  });
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  HTMLMediaElement.prototype.pause = vi.fn();
  const minted: Mint[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
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
      if (path === "/api/albums/lake") return Response.json(album);
      if (path === "/api/albums/lake/videos")
        return Response.json({ entries: [party], next_cursor: "" });
      if (path === "/api/media/signed") {
        minted.push(JSON.parse(String(init?.body)) as Mint);
        return refuse
          ? Response.json({ error: {} }, { status: 404 })
          : Response.json({ url: signedURL });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums/lake/videos/video-1");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Video 1 of 1" });
  const video = within(dialog).getByLabelText<AirPlayVideo>("Birthday party");
  const announce = (availability: string) =>
    fireEvent(
      video,
      Object.assign(new Event("webkitplaybacktargetavailabilitychanged"), {
        availability,
      }),
    );
  const wireless = (on: boolean) => {
    video.webkitCurrentPlaybackTargetIsWireless = on;
    fireEvent(video, new Event("webkitcurrentplaybacktargetiswirelesschanged"));
  };
  return { dialog, video, minted, announce, wireless };
}

it("plays a signed URL from the start where AirPlay exists, so the source never changes on the way to the TV", async () => {
  const { dialog, video, minted, announce, wireless } = await openVideo(false);
  const user = userEvent.setup();
  // The video loads once, from a URL an Apple TV can fetch too. Changing the
  // source after the video reaches the TV drops the route, which Safari then
  // rebuilds, in a loop.
  expect(video).toHaveAttribute("x-webkit-airplay", "allow");
  await waitFor(() => expect(video).toHaveAttribute("src", signedURL));
  expect(minted).toEqual([{ entry_id: "video-1", variant: "playback" }]);
  // The TV names the video, not the web page it came from.
  expect(navigator.mediaSession.metadata?.title).toBe("Birthday party");

  // The button waits for Safari to find a target on the network.
  expect(
    within(dialog).queryByRole("button", { name: "AirPlay" }),
  ).not.toBeInTheDocument();
  video.webkitShowPlaybackTargetPicker = vi.fn();
  announce("available");
  await user.click(within(dialog).getByRole("button", { name: "AirPlay" }));
  expect(video.webkitShowPlaybackTargetPicker).toHaveBeenCalledOnce();

  wireless(true);
  expect(video).toHaveAttribute("src", signedURL);
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  // Chapters still seek the local element, which AirPlay mirrors.
  await user.click(within(dialog).getByRole("combobox", { name: "Chapter" }));
  await user.click(screen.getByRole("option", { name: /Goodbyes/ }));
  expect(video.currentTime).toBe(4);
  wireless(false);
  expect(video).toHaveAttribute("src", signedURL);
  expect(minted).toHaveLength(1);

  announce("not-available");
  expect(
    within(dialog).queryByRole("button", { name: "AirPlay" }),
  ).not.toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Close video" }));
  await waitFor(() => expect(navigator.mediaSession.metadata).toBeNull());
});

it("plays the cookie URL when no signed URL can be had, and says so if that video reaches a TV", async () => {
  const { dialog, video, wireless } = await openVideo(true);
  await waitFor(() => expect(video).toHaveAttribute("src", party.playback_url));
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  wireless(true);
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(
    "Could not send this video to the TV.",
  );
  expect(video).toHaveAttribute("src", party.playback_url);
  wireless(false);
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
});
