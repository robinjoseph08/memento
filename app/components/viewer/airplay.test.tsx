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

it("offers AirPlay where Safari reports a target and gives the video a signed URL before it reaches the TV", async () => {
  // jsdom has no media pipeline: position is a property and playing a promise.
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
  const minted: unknown[] = [];
  let refuse = false;
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
      if (path === "/api/media/signed" && refuse)
        return Response.json({ error: {} }, { status: 404 });
      if (path === "/api/media/signed") {
        minted.push(JSON.parse(String(init?.body)));
        return Response.json({ url: signedURL });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/videos/video-1");
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Video 1 of 1" });
  const video = within(dialog).getByLabelText<AirPlayVideo>("Birthday party");
  expect(video).toHaveAttribute("x-webkit-airplay", "allow");
  expect(
    within(dialog).queryByRole("button", { name: "AirPlay" }),
  ).not.toBeInTheDocument();

  const announce = () =>
    fireEvent(
      video,
      Object.assign(new Event("webkitplaybacktargetavailabilitychanged"), {
        availability: "available",
      }),
    );
  const wireless = (on: boolean) => {
    video.webkitCurrentPlaybackTargetIsWireless = on;
    fireEvent(video, new Event("webkitcurrentplaybacktargetiswirelesschanged"));
  };

  // Safari announces a target on the network; other browsers never do. A TV
  // that cannot be given a URL is reported instead of left spinning.
  video.webkitShowPlaybackTargetPicker = vi.fn();
  refuse = true;
  announce();
  await user.click(within(dialog).getByRole("button", { name: "AirPlay" }));
  expect(video.webkitShowPlaybackTargetPicker).toHaveBeenCalledOnce();
  wireless(true);
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(
    "Could not send this video to the TV.",
  );
  expect(video).toHaveAttribute("src", party.playback_url);
  wireless(false);
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();

  // The signed URL goes in as soon as a target exists, at the same position,
  // so the source never has to change while the video is on the TV: changing
  // it there drops the route, which Safari then rebuilds, in a loop.
  refuse = false;
  video.currentTime = 3;
  announce();
  await waitFor(() => expect(video).toHaveAttribute("src", signedURL));
  expect(minted).toEqual([{ entry_id: "video-1", variant: "playback" }]);
  video.currentTime = 0;
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(3);
  announce();
  wireless(true);
  expect(video).toHaveAttribute("src", signedURL);
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();

  // Chapters still seek the local element, which AirPlay mirrors.
  await user.click(within(dialog).getByRole("combobox", { name: "Chapter" }));
  await user.click(screen.getByRole("option", { name: /Goodbyes/ }));
  expect(video.currentTime).toBe(4);

  // Coming back from the TV keeps the same source too.
  wireless(false);
  expect(video).toHaveAttribute("src", signedURL);
  expect(minted).toHaveLength(1);

  fireEvent(
    video,
    Object.assign(new Event("webkitplaybacktargetavailabilitychanged"), {
      availability: "not-available",
    }),
  );
  expect(
    within(dialog).queryByRole("button", { name: "AirPlay" }),
  ).not.toBeInTheDocument();
});
