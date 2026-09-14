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
  AlbumDetail,
  ExcludeEntriesRequest,
  IncludeEntryRequest,
} from "../../types/generated/publishing";

const person = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};

const entry = {
  decisions: {},
  kind: "IMAGE",
  available: true,
  title: "",
  chapters: [],
  chapter_status: "",
  chapter_message: "",
  playback_url: "",
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
  processed: 2,
  total: 2,
  photo_count: 2,
  video_count: 0,
  start_date: "2026-07-01",
  end_date: "2026-07-01",
  cover_url: "/media/beach",
  access: [],
  excluded: [],
  moments: [
    {
      id: "day-1",
      title: "First day",
      label: "First day",
      date: "2026-07-01",
      end_date: "2026-07-01",
      cover_entry_id: "beach",
      cover_position: 0,
      access: { people: [], faces: [] },
      entries: [
        {
          ...entry,
          id: "beach",
          media_id: "m-beach",
          filename: "Beach.jpg",
          captured_at: "2026-07-01T12:00:00",
          thumbnail_url: "/media/beach",
        },
        {
          ...entry,
          id: "dunes",
          media_id: "m-dunes",
          filename: "Dunes.jpg",
          captured_at: "2026-07-01T13:00:00",
          thumbnail_url: "/media/dunes",
        },
      ],
    },
  ],
};

const excludedAlbum: AlbumDetail = {
  ...album,
  photo_count: 1,
  excluded: [
    {
      id: "beach",
      filename: "Beach.jpg",
      kind: "IMAGE",
      captured_at: "2026-07-01T12:00:00",
      thumbnail_url: "/api/media/sources/assets/beach/thumbnail",
      available: true,
      excluded_at: "2026-07-02T00:00:00Z",
    },
  ],
  moments: [
    {
      ...album.moments[0]!,
      cover_entry_id: "dunes",
      cover_position: 0,
      entries: [album.moments[0]!.entries[1]!],
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

function mockAPI(handler: (path: string, options?: RequestInit) => Response) {
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
            version: "3.1.0",
            message: "",
          }),
        );
      if (path.endsWith("/access-requests"))
        return Promise.resolve(Response.json([]));
      if (path.endsWith("/faces/refresh"))
        return Promise.resolve(Response.json(album));
      return Promise.resolve(handler(path, options));
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("keeps selected media out after a visibility review and lists it as excluded", async () => {
  desktopViewport();
  let current = album;
  let excluded: ExcludeEntriesRequest | undefined;
  mockAPI((path, options) => {
    if (path.endsWith("/exclude/preview"))
      return Response.json({
        ready: true,
        review_token: "reviewed-exclusion",
        removes_moment: false,
        changes: [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: [],
            lost_entry_ids: ["beach"],
          },
        ],
        conflicts: [],
      });
    if (path.endsWith("/exclude")) {
      excluded = JSON.parse(String(options?.body)) as ExcludeEntriesRequest;
      current = excludedAlbum;
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?moment=day-1&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  const momentPane = await screen.findByRole("region", { name: "First day" });
  await user.click(within(momentPane).getByRole("button", { name: "Select" }));
  await user.click(
    within(momentPane).getByRole("checkbox", { name: "Select Beach.jpg" }),
  );
  await user.click(
    within(momentPane).getByRole("button", { name: "Keep out" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Keep 1 item out of this album?",
  });
  expect(within(dialog).getByText(/becomes the cover/)).toBeVisible();
  const visibility = within(dialog).getByRole("region", {
    name: "Visibility review",
  });
  expect(await within(visibility).findByText(/loses 1/)).toBeVisible();
  await user.click(within(dialog).getByRole("button", { name: "Keep out" }));
  await waitFor(() =>
    expect(excluded).toEqual({
      entry_ids: ["beach"],
      review_token: "reviewed-exclusion",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  const outline = screen.getByRole("navigation", { name: "Album outline" });
  const excludedLink = within(outline).getByRole("link", {
    name: /Excluded media/,
  });
  expect(excludedLink).toHaveTextContent("1");
  await user.click(excludedLink);
  const list = await screen.findByRole("list", { name: "Excluded media" });
  expect(within(list).getByText("Beach.jpg")).toBeVisible();
});

it("adds excluded media back into a chosen Moment after a review", async () => {
  desktopViewport();
  let current = excludedAlbum;
  const previews: IncludeEntryRequest[] = [];
  let included: IncludeEntryRequest | undefined;
  mockAPI((path, options) => {
    if (path.endsWith("/include/preview")) {
      const request = JSON.parse(String(options?.body)) as IncludeEntryRequest;
      previews.push(request);
      return Response.json({
        ready: true,
        review_token: `reviewed:${request.moment_id}`,
        removes_moment: false,
        changes: [],
        conflicts: [],
      });
    }
    if (path.endsWith("/include")) {
      included = JSON.parse(String(options?.body)) as IncludeEntryRequest;
      current = album;
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=excluded&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  const list = await screen.findByRole("list", { name: "Excluded media" });
  await user.click(within(list).getByRole("button", { name: "Add back" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Add Beach.jpg back?",
  });
  const destination = within(dialog).getByRole("combobox", { name: "Moment" });
  await waitFor(() => expect(destination).toHaveTextContent("First day"));
  await waitFor(() => expect(previews).toHaveLength(1));
  await user.click(destination);
  await user.click(screen.getByRole("option", { name: "New Moment: Jul 1" }));
  await waitFor(() => expect(previews).toHaveLength(2));
  expect(previews[1]?.moment_id).toBe("new:2026-07-01");
  await user.click(within(dialog).getByRole("button", { name: "Add back" }));
  await waitFor(() =>
    expect(included).toEqual({
      moment_id: "new:2026-07-01",
      review_token: "reviewed:new:2026-07-01",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(await screen.findByText(/Nothing is kept out/)).toBeVisible();
});

it("keeps a photo out from its details dialog without a second confirmation", async () => {
  desktopViewport();
  let current = album;
  let excluded: ExcludeEntriesRequest | undefined;
  mockAPI((path, options) => {
    if (path.endsWith("/exclude/preview"))
      return Response.json({
        ready: true,
        review_token: "reviewed-exclusion",
        removes_moment: false,
        changes: [],
        conflicts: [],
      });
    if (path.endsWith("/exclude")) {
      excluded = JSON.parse(String(options?.body)) as ExcludeEntriesRequest;
      current = excludedAlbum;
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?moment=day-1&pane=detail&entry=beach",
  );
  const user = userEvent.setup();
  render(<App />);
  const details = await screen.findByRole("dialog", { name: "Photo details" });
  await user.click(
    within(details).getByRole("button", { name: "Keep out of this album" }),
  );
  expect(
    screen.queryByRole("dialog", { name: /Keep 1 item out/ }),
  ).not.toBeInTheDocument();
  await waitFor(() => expect(excluded?.entry_ids).toEqual(["beach"]));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(
    within(screen.getByRole("navigation", { name: "Album outline" })).getByRole(
      "link",
      { name: /Excluded/ },
    ),
  ).toHaveTextContent("1");
});
