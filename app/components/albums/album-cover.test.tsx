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
  SaveCoverOrderRequest,
  ViewingGroups,
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
  ready: true,
  status: "complete",
  message: "",
  processed: 2,
  total: 2,
  photo_count: 2,
  video_count: 0,
  start_date: "2026-07-01",
  end_date: "2026-07-02",
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
      ],
    },
    {
      id: "day-2",
      title: "Second day",
      label: "Second day",
      date: "2026-07-02",
      end_date: "2026-07-02",
      cover_entry_id: "dunes",
      cover_position: 0,
      access: { people: [], faces: [] },
      entries: [
        {
          ...entry,
          id: "dunes",
          media_id: "m-dunes",
          filename: "Dunes.jpg",
          captured_at: "2026-07-02T13:00:00",
          thumbnail_url: "/media/dunes",
        },
      ],
    },
  ],
};

const viewer = (id: string, name: string) => ({
  person_id: id,
  display_name: name,
  avatar_url: "",
});

const groups: ViewingGroups = {
  groups: [
    {
      moment_ids: ["day-1", "day-2"],
      people: [viewer("alex", "Alex"), viewer("sam", "Sam")],
    },
    { moment_ids: ["day-2"], people: [viewer("kim", "Kim")] },
  ],
  placeholder: [viewer("pat", "Pat")],
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
      if (path.endsWith("/viewing-groups"))
        return Promise.resolve(Response.json(groups));
      return Promise.resolve(handler(path, options));
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("previews each Viewing Group's cover for the draft order, guards publishing, and saves the order", async () => {
  desktopViewport();
  let current = album;
  let saved: SaveCoverOrderRequest | undefined;
  mockAPI((path, options) => {
    if (path.endsWith("/cover-order")) {
      saved = JSON.parse(String(options?.body)) as SaveCoverOrderRequest;
      current = {
        ...album,
        moments: album.moments.map((moment) => ({
          ...moment,
          cover_position: saved?.moment_ids.indexOf(moment.id) === 0 ? 1 : 0,
        })),
      };
    }
    return Response.json(current);
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/album-1?section=cover&pane=detail",
  );
  const user = userEvent.setup();
  render(<App />);
  const pane = await screen.findByRole("region", { name: "Album cover" });
  const preferred = within(pane).getByRole("region", {
    name: "Preferred covers",
  });
  expect(within(preferred).getByText(/No preferred Moments yet/)).toBeVisible();
  const captureOrder = within(pane).getByRole("region", {
    name: "Then in capture order",
  });
  expect(
    within(captureOrder)
      .getAllByRole("listitem")
      .map((item) => item.getAttribute("aria-label")),
  ).toEqual(["First day", "Second day"]);
  const who = within(pane).getByRole("region", {
    name: "Who sees which cover",
  });
  const rows = await within(who).findAllByRole("listitem");
  expect(rows[0]).toHaveTextContent("2 people see the cover of First day");
  expect(rows[0]).toHaveTextContent("Can see First day, Second day");
  expect(
    within(rows[0]!).getByRole("img", { name: "Allowed: Alex, Sam" }),
  ).toBeVisible();
  expect(rows[1]).toHaveTextContent("1 person sees the cover of Second day");
  expect(rows[2]).toHaveTextContent("1 person sees a placeholder");
  const save = within(pane).getByRole("button", { name: "Save cover order" });
  expect(save).toBeDisabled();

  await user.click(
    within(captureOrder).getByRole("button", { name: "Prefer Second day" }),
  );
  expect(
    within(preferred)
      .getAllByRole("listitem")
      .map((item) => item.getAttribute("aria-label")),
  ).toEqual(["Second day"]);
  expect(within(preferred).getByText("1")).toBeVisible();
  expect(within(who).getAllByRole("listitem")[0]).toHaveTextContent(
    "2 people see the cover of Second day",
  );
  expect(save).toBeEnabled();

  // Publishing with the draft unsaved prompts first.
  await user.click(screen.getByRole("button", { name: "Review & publish" }));
  const prompt = await screen.findByRole("dialog", {
    name: "Continue without saving?",
  });
  await user.click(within(prompt).getByRole("button", { name: "Cancel" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Continue without saving?" }),
    ).toBeNull(),
  );
  expect(save).toBeEnabled();

  await user.click(save);
  await waitFor(() => expect(saved).toEqual({ moment_ids: ["day-2"] }));
  expect(await within(pane).findByRole("status")).toHaveTextContent(
    "Cover order saved.",
  );
  expect(save).toBeDisabled();
  expect(
    within(preferred)
      .getAllByRole("listitem")
      .map((item) => item.getAttribute("aria-label")),
  ).toEqual(["Second day"]);

  await user.click(
    within(preferred).getByRole("button", {
      name: "Remove Second day from preferred covers",
    }),
  );
  expect(within(preferred).getByText(/No preferred Moments yet/)).toBeVisible();
  expect(within(who).getAllByRole("listitem")[0]).toHaveTextContent(
    "2 people see the cover of First day",
  );
  expect(save).toBeEnabled();
});
