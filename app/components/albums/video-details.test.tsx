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
import type { AlbumDetail, Entry } from "../../types/generated/publishing";

const person = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};
const video: Entry = {
  id: "video",
  decisions: {},
  media_id: "m-video",
  filename: "Waves at dusk.mp4",
  kind: "VIDEO",
  captured_at: "2026-07-01T13:00:00",
  available: true,
  thumbnail_url: "/media/waves",
  title: "",
  chapters: [],
  chapter_status: "failed",
  chapter_message: "Chapter extraction failed. Playback still works.",
  playback_url: "/api/media/entries/video/playback?v=1",
};
const album: AlbumDetail = {
  id: "album-1",
  source_id: "summer",
  title: "Summer by the sea",
  description: "",
  published: false,
  ready: false,
  status: "complete",
  message: "",
  processed: 1,
  total: 1,
  photo_count: 0,
  video_count: 1,
  start_date: "2026-07-01",
  end_date: "2026-07-01",
  cover_url: "",
  access: [],
  moments: [
    {
      id: "day-1",
      title: "",
      label: "July 1, 2026",
      date: "2026-07-01",
      end_date: "2026-07-01",
      cover_entry_id: "video",
      access: { people: [], faces: [] },
      entries: [video],
    },
  ],
};

function desktopViewport() {
  vi.stubGlobal("matchMedia", (media: string) => ({
    media,
    matches: /min-width/.test(media),
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

function mockAPI(state: { album: AlbumDetail; posts: [string, unknown][] }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Promise.resolve(
          Response.json({ claimed: true, person, auth_mode: "fake" }),
        );
      if (path.endsWith("/connection"))
        return Promise.resolve(
          Response.json({ usable: true, version: "3.1.0", message: "" }),
        );
      if (options?.method === "POST") {
        const body: unknown = JSON.parse(String(options.body));
        state.posts.push([path, body]);
        if (path.endsWith("/entries/video/video")) {
          const title = (body as { title: string }).title;
          state.album = {
            ...state.album,
            moments: [
              {
                ...state.album.moments[0],
                entries: [{ ...video, title, chapter_status: "failed" }],
              },
            ],
          };
        }
        if (path.endsWith("/chapters/retry")) {
          state.album = {
            ...state.album,
            moments: [
              {
                ...state.album.moments[0],
                entries: [
                  {
                    ...state.album.moments[0].entries[0],
                    chapter_status: "pending",
                    chapter_message: "",
                    playback_url: "",
                  },
                ],
              },
            ],
          };
        }
        return Promise.resolve(Response.json(state.album));
      }
      return Promise.resolve(Response.json(state.album));
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("edits a video title with the filename as fallback and retries failed chapters", async () => {
  desktopViewport();
  const state = { album, posts: [] as [string, unknown][] };
  mockAPI(state);
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const moment = await screen.findByRole("region", {
    name: "Wednesday, July 1, 2026",
  });
  expect(
    within(moment).getByRole("img", { name: "Chapter extraction failed" }),
  ).toBeVisible();
  // The tile opens everything about the video: title, chapters, and access.
  await user.click(
    within(moment).getByRole("button", { name: "Edit Waves at dusk.mp4" }),
  );
  const dialog = await screen.findByRole("dialog", { name: "Video details" });
  expect(
    within(dialog).getByRole("form", { name: "Item access" }),
  ).toBeVisible();
  const player =
    within(dialog).getByLabelText<HTMLVideoElement>("Waves at dusk.mp4");
  expect(player.tagName).toBe("VIDEO");
  expect(player).toHaveAttribute("src", video.playback_url);
  expect(player).toHaveAttribute("controls");
  expect(player).not.toHaveAttribute("autoplay");
  const title = within(dialog).getByRole("textbox", { name: "Video title" });
  expect(title).toHaveValue("");
  expect(title).toHaveAttribute("placeholder", "Waves at dusk");
  expect(within(dialog).getByRole("alert")).toHaveTextContent(
    "Chapter extraction failed. Playback still works.",
  );
  await user.click(
    within(dialog).getByRole("button", { name: "Retry chapters" }),
  );
  await waitFor(() =>
    expect(state.posts).toContainEqual([
      "/api/curator/albums/album-1/entries/video/chapters/retry",
      {},
    ]),
  );
  expect(await within(dialog).findByRole("status")).toHaveTextContent(
    "Reading chapters from the video.",
  );
  await user.type(title, "Evening waves");
  await user.click(within(dialog).getByRole("button", { name: "Save title" }));
  await waitFor(() =>
    expect(state.posts).toContainEqual([
      "/api/curator/albums/album-1/entries/video/video",
      { title: "Evening waves" },
    ]),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Video details" }),
    ).not.toBeInTheDocument(),
  );
  expect(
    screen.queryByRole("dialog", { name: "Leave this page?" }),
  ).not.toBeInTheDocument();
  // Reopening shows the saved title; clearing it sends an empty title.
  await user.click(
    within(moment).getByRole("button", { name: "Edit Waves at dusk.mp4" }),
  );
  const reopened = await screen.findByRole("dialog", { name: "Video details" });
  const saved = within(reopened).getByRole("textbox", { name: "Video title" });
  expect(saved).toHaveValue("Evening waves");
  await user.clear(saved);
  await user.click(
    within(reopened).getByRole("button", { name: "Save title" }),
  );
  await waitFor(() =>
    expect(state.posts).toContainEqual([
      "/api/curator/albums/album-1/entries/video/video",
      { title: "" },
    ]),
  );
});

it("asks before discarding an edited title", async () => {
  desktopViewport();
  const state = { album, posts: [] as [string, unknown][] };
  mockAPI(state);
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?moment=day-1&entry=video",
  );
  const user = userEvent.setup();
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Video details" });
  await user.type(
    within(dialog).getByRole("textbox", { name: "Video title" }),
    "Draft",
  );
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  const confirm = await screen.findByRole("dialog", {
    name: "Discard these changes?",
  });
  await user.click(within(confirm).getByRole("button", { name: "Discard" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Video details" }),
    ).not.toBeInTheDocument(),
  );
  expect(state.posts.filter(([path]) => path.endsWith("/video"))).toEqual([]);
});

it("narrows a Moment to its photos or videos from the kind tabs", async () => {
  desktopViewport();
  const photo: Entry = {
    ...video,
    id: "photo",
    media_id: "m-photo",
    filename: "Beach.jpg",
    kind: "IMAGE",
    chapter_status: "",
    chapter_message: "",
    playback_url: "",
  };
  const state = {
    album: {
      ...album,
      photo_count: 1,
      moments: [{ ...album.moments[0], entries: [photo, video] }],
    },
    posts: [] as [string, unknown][],
  };
  mockAPI(state);
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  const moment = await screen.findByRole("region", {
    name: "Wednesday, July 1, 2026",
  });
  const tabs = within(moment).getByRole("navigation", {
    name: "Moment media kind",
  });
  expect(within(tabs).getByRole("link", { name: "All 2" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(within(moment).getByText("2 of 2 items shown")).toBeVisible();
  await user.click(within(tabs).getByRole("link", { name: "Videos 1" }));
  expect(new URLSearchParams(window.location.search).get("kind")).toBe(
    "videos",
  );
  expect(
    within(moment).getByRole("img", { name: "Waves at dusk.mp4" }),
  ).toBeVisible();
  expect(
    within(moment).queryByRole("img", { name: "Beach.jpg" }),
  ).not.toBeInTheDocument();
  expect(within(moment).getByText("1 of 1 item shown")).toBeVisible();
  // Select all reaches only the videos on screen.
  await user.click(within(moment).getByRole("button", { name: "Select" }));
  await user.click(
    within(moment).getByRole("checkbox", { name: "Select all 1 items" }),
  );
  expect(within(moment).getByText("1 selected")).toBeVisible();
  expect(
    within(moment).getByRole("button", { name: "Video details" }),
  ).toBeVisible();
  await user.click(within(tabs).getByRole("link", { name: "Photos 1" }));
  expect(within(moment).getByRole("img", { name: "Beach.jpg" })).toBeVisible();
  expect(
    within(moment).queryByRole("img", { name: "Waves at dusk.mp4" }),
  ).not.toBeInTheDocument();
  await user.click(within(tabs).getByRole("link", { name: "All 2" }));
  expect(new URLSearchParams(window.location.search).get("kind")).toBeNull();
  expect(
    within(moment).getAllByRole("img", { name: /\.(jpg|mp4)$/ }),
  ).toHaveLength(2);
});

it("keeps the dialog open after saving access while a title edit is unsaved", async () => {
  desktopViewport();
  const alex = {
    person_id: "alex",
    display_name: "Alex",
    avatar_url: "",
    decision: "",
    detected: false,
    suggested: false,
    supporting_entries: 0,
    inherited: false,
    effective: false,
    accessible_count: 0,
    exceptions: 0,
    moments_detected: 0,
    deactivated: false,
  };
  const state = {
    album: {
      ...album,
      moments: [{ ...album.moments[0], access: { people: [alex], faces: [] } }],
    },
    posts: [] as [string, unknown][],
  };
  mockAPI(state);
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?moment=day-1&entry=video",
  );
  const user = userEvent.setup();
  render(<App />);
  const dialog = await screen.findByRole("dialog", { name: "Video details" });
  const title = within(dialog).getByRole("textbox", { name: "Video title" });
  await user.type(title, "Party");
  // Saving access with nothing changed there behaves like Cancel: it asks
  // about the unsaved title instead of handing the shell a blocked navigation.
  await user.click(within(dialog).getByRole("button", { name: "Save access" }));
  const confirm = await screen.findByRole("dialog", {
    name: "Discard these changes?",
  });
  await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
  expect(screen.getByRole("dialog", { name: "Video details" })).toBeVisible();
  expect(
    screen.queryByRole("dialog", { name: "Leave this page?" }),
  ).not.toBeInTheDocument();
  // A real access save keeps the dialog open for the unsaved title, and typing
  // back to the saved value does not close it either.
  await user.click(
    within(dialog).getByRole("combobox", { name: "Access for Alex" }),
  );
  await user.click(screen.getByRole("option", { name: "Allow" }));
  await user.click(within(dialog).getByRole("button", { name: "Save access" }));
  await waitFor(() =>
    expect(state.posts.some(([path]) => path.endsWith("/rules"))).toBe(true),
  );
  expect(screen.getByRole("dialog", { name: "Video details" })).toBeVisible();
  await user.clear(title);
  expect(title).toHaveValue("");
  expect(screen.getByRole("dialog", { name: "Video details" })).toBeVisible();
  expect(
    screen.queryByRole("dialog", { name: "Leave this page?" }),
  ).not.toBeInTheDocument();
});
