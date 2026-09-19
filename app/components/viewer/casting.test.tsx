import {
  act,
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
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";

const album: ViewerAlbum = {
  id: "lake",
  title: "A weekend by the lake",
  description: "",
  photo_count: 2,
  video_count: 1,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "",
  cover_preview_url: "",
  days: [
    {
      date: "2025-06-14",
      photo_count: 2,
      video_count: 1,
      photo_ratios: [1.5, 1.5],
      video_ratios: [1.78],
    },
  ],
};
const lake: ViewerEntry = {
  id: "photo-1",
  kind: "IMAGE",
  title: "Lake",
  captured_at: "2025-06-14T00:15:00Z",
  available: true,
  thumbnail_url: "/media/lake/photo-1/thumb",
  preview_url: "/media/lake/photo-1",
  download_url: "/media/lake/photo-1/original",
  playback_url: "",
  width: 1200,
  height: 800,
  chapters: [],
  chapter_status: "",
};
const cabin: ViewerEntry = {
  ...lake,
  id: "photo-2",
  title: "Cabin",
  thumbnail_url: "/media/lake/photo-2/thumb",
  preview_url: "/media/lake/photo-2",
};
const party: ViewerEntry = {
  ...lake,
  id: "video-1",
  kind: "VIDEO",
  title: "Birthday party",
  thumbnail_url: "/media/lake/video-1/thumb",
  preview_url: "/media/lake/video-1",
  playback_url: "/media/lake/video-1/playback",
  chapter_status: "complete",
  chapters: [{ title: "Arrival", start: 0, end: 2 }],
};

// The slice of Google's sender SDK the app touches. The real one is a script
// from gstatic that only does anything in Chrome with a receiver nearby.
function fakeCastSDK() {
  let state = "NO_DEVICES_AVAILABLE";
  let device = "";
  const listeners = new Set<() => void>();
  const loaded: { url: string; contentType: string; title: string }[] = [];
  let playing: unknown = null;
  const failing = { next: false };
  const emit = (next: string, name = "", media: unknown = null) => {
    state = next;
    device = name;
    playing = media;
    for (const listener of listeners) listener();
  };
  const session = {
    getCastDevice: () => ({ friendlyName: device }),
    getMediaSession: () => (playing ? { media: playing } : null),
    loadMedia: vi.fn(
      (request: {
        media: {
          contentId: string;
          contentType: string;
          metadata: { title: string };
        };
      }) => {
        if (failing.next) {
          failing.next = false;
          return Promise.reject(new Error("load_media_failed"));
        }
        loaded.push({
          url: request.media.contentId,
          contentType: request.media.contentType,
          title: request.media.metadata.title,
        });
        return Promise.resolve();
      },
    ),
  };
  const context = {
    setOptions: vi.fn(),
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    getCastState: () => state,
    getCurrentSession: () => (state === "CONNECTED" ? session : null),
    requestSession: vi.fn(() => {
      emit("CONNECTED", "Living room TV");
      return Promise.resolve();
    }),
    endCurrentSession: vi.fn(() => emit("NOT_CONNECTED")),
  };
  vi.stubGlobal("cast", {
    framework: {
      CastContext: { getInstance: () => context },
      CastContextEventType: { CAST_STATE_CHANGED: "caststatechanged" },
      CastState: {
        NO_DEVICES_AVAILABLE: "NO_DEVICES_AVAILABLE",
        CONNECTED: "CONNECTED",
      },
    },
  });
  vi.stubGlobal("chrome", {
    cast: {
      AutoJoinPolicy: { ORIGIN_SCOPED: "origin_scoped" },
      media: {
        DEFAULT_MEDIA_RECEIVER_APP_ID: "CC1AD845",
        MediaInfo: class {
          metadata: unknown;
          constructor(
            readonly contentId: string,
            readonly contentType: string,
          ) {}
        },
        GenericMediaMetadata: class {
          title = "";
        },
        LoadRequest: class {
          constructor(readonly media: unknown) {}
        },
      },
    },
  });
  return { context, emit, loaded, failing };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/");
});

it("offers Cast only with a receiver nearby, follows the lightbox on the TV, and stops on request", async () => {
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  HTMLMediaElement.prototype.pause = vi.fn();
  const sdk = fakeCastSDK();
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
      if (init?.method === "HEAD")
        return new Response(null, {
          headers: {
            "Content-Type": path.endsWith("/playback")
              ? "video/mp4"
              : "image/webp",
          },
        });
      if (path === "/api/albums/lake") return Response.json(album);
      if (path === "/api/albums/lake/photos")
        return Response.json({ entries: [lake, cabin], next_cursor: "" });
      if (path === "/api/albums/lake/videos")
        return Response.json({ entries: [party], next_cursor: "" });
      if (path === "/api/media/signed") {
        const body = JSON.parse(String(init?.body)) as {
          entry_id: string;
          variant: string;
        };
        return Response.json({
          url: `https://memento.example/signed/${body.variant}/${body.entry_id}`,
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const user = userEvent.setup();
  window.history.replaceState(null, "", "/albums/lake/photos/photo-1");
  render(<App />);
  let dialog = await screen.findByRole("dialog", { name: "Photo 1 of 2" });
  const script = document.querySelector<HTMLScriptElement>(
    'script[src*="cast_sender.js"]',
  );
  expect(script?.src).toBe(
    "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1",
  );
  expect(
    within(dialog).queryByRole("button", { name: "Cast" }),
  ).not.toBeInTheDocument();

  // The SDK arrives and finds no receiver: still nothing to show.
  act(() => window.__onGCastApiAvailable(true));
  expect(sdk.context.setOptions).toHaveBeenCalledWith({
    receiverApplicationId: "CC1AD845",
    autoJoinPolicy: "origin_scoped",
  });
  expect(
    within(dialog).queryByRole("button", { name: "Cast" }),
  ).not.toBeInTheDocument();

  act(() => sdk.emit("NOT_CONNECTED"));
  await user.click(within(dialog).getByRole("button", { name: "Cast" }));
  expect(sdk.context.requestSession).toHaveBeenCalledOnce();
  await waitFor(() =>
    expect(sdk.loaded).toEqual([
      {
        url: "https://memento.example/signed/preview/photo-1",
        contentType: "image/webp",
        title: "Lake",
      },
    ]),
  );
  expect(await within(dialog).findByRole("status")).toHaveTextContent(
    "Showing Lake on Living room TV",
  );
  expect(
    within(dialog).queryByRole("button", { name: "Cast" }),
  ).not.toBeInTheDocument();

  // A receiver that refuses an item is reported, never papered over, and
  // the next step tries again.
  sdk.failing.next = true;
  await user.keyboard("{ArrowRight}");
  dialog = await screen.findByRole("dialog", { name: "Photo 2 of 2" });
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(
    "Could not show this photo on Living room TV.",
  );
  expect(within(dialog).queryByRole("status")).not.toBeInTheDocument();
  await user.keyboard("{ArrowLeft}");
  dialog = await screen.findByRole("dialog", { name: "Photo 1 of 2" });
  await waitFor(() =>
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      "Showing Lake on Living room TV",
    ),
  );

  // Stepping through the lightbox moves the TV along with it.
  await user.keyboard("{ArrowRight}");
  dialog = await screen.findByRole("dialog", { name: "Photo 2 of 2" });
  await waitFor(() =>
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      "Showing Cabin on Living room TV",
    ),
  );
  expect(sdk.loaded.at(-1)?.url).toBe(
    "https://memento.example/signed/preview/photo-2",
  );

  // Closing the lightbox leaves the TV alone.
  await user.click(within(dialog).getByRole("button", { name: "Close photo" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(sdk.context.endCurrentSession).not.toHaveBeenCalled();

  // A video opened during the session plays on the TV, not on both screens.
  await user.click(screen.getByRole("link", { name: /Videos/ }));
  await user.click(
    await screen.findByRole("link", { name: "Open video Birthday party" }),
  );
  dialog = await screen.findByRole("dialog", { name: "Video 1 of 1" });
  await waitFor(() =>
    expect(sdk.loaded.at(-1)).toEqual({
      url: "https://memento.example/signed/playback/video-1",
      contentType: "video/mp4",
      title: "Birthday party",
    }),
  );
  expect(within(dialog).getByLabelText("Birthday party")).not.toHaveAttribute(
    "autoplay",
  );
  expect(sdk.loaded).toHaveLength(4);
  // Chapters would only seek the paused player here, so they wait for stop.
  expect(
    within(dialog).queryByRole("combobox", { name: "Chapter" }),
  ).not.toBeInTheDocument();

  await user.click(
    within(dialog).getByRole("button", { name: "Stop casting" }),
  );
  expect(sdk.context.endCurrentSession).toHaveBeenCalledWith(true);
  await waitFor(() =>
    expect(within(dialog).queryByRole("status")).not.toBeInTheDocument(),
  );
  expect(within(dialog).getByRole("button", { name: "Cast" })).toBeVisible();
  expect(
    within(dialog).getByRole("combobox", { name: "Chapter" }),
  ).toBeVisible();

  // A session joined after a reload already shows this video, so it is named
  // and not loaded again from the start.
  act(() =>
    sdk.emit("CONNECTED", "Den TV", {
      customData: { entryID: "video-1", title: "Birthday party" },
    }),
  );
  expect(await within(dialog).findByRole("status")).toHaveTextContent(
    "Showing Birthday party on Den TV",
  );
  expect(sdk.loaded).toHaveLength(4);
});
