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
  SyncRequest,
  SyncReview,
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
  description: "A week away",
  published: true,
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
  moments: [
    {
      id: "day-1",
      title: "First day",
      label: "First day",
      date: "2026-07-01",
      end_date: "2026-07-01",
      cover_entry_id: "beach",
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

const emptyReview: SyncReview = {
  ready: false,
  up_to_date: true,
  review_token: "same",
  description: null,
  additions: [],
  removals: [],
  changes: [],
  cover_choices: [],
  removed_moments: [],
  moments: [{ id: "day-1", label: "First day", new: false }],
  audience: [],
  blockers: [],
  faces_refreshed: true,
  faces_message: "",
};

// The backend recomputes the review for every request; this stand-in echoes
// the Curator's decisions the same way.
function reviewFor(request: SyncRequest): SyncReview {
  const placement = request.placements.find(
    (item) => item.source_id === "asset-new",
  );
  const cover = request.covers.find((item) => item.moment_id === "day-1");
  const exclude = placement?.exclude ?? false;
  const momentID = placement?.moment_id || "day-1";
  const blockers = cover ? [] : ["Choose a new cover for First day."];
  return {
    ...emptyReview,
    ready: blockers.length === 0,
    up_to_date: false,
    review_token: `token:${momentID}:${exclude}:${cover?.entry_id ?? ""}`,
    description: { before: "A week away", after: "A week by the sea" },
    additions: [
      {
        source_id: "asset-new",
        filename: "Sunset.jpg",
        kind: "IMAGE",
        captured_at: "2026-07-01T20:00:00",
        thumbnail_url: "/api/media/sources/assets/asset-new/thumbnail",
        returning: false,
        previously_excluded: false,
        suggested_moment_id: "day-1",
        moment_id: exclude ? "day-1" : momentID,
        exclude,
        other_albums: [],
      },
    ],
    removals: [
      {
        entry_id: "beach",
        filename: "Beach.jpg",
        kind: "IMAGE",
        captured_at: "2026-07-01T12:00:00",
        thumbnail_url: "/media/beach",
        moment_id: "day-1",
        moment_label: "First day",
        cover: true,
        deleted: false,
      },
    ],
    changes: [
      {
        entry_id: "dunes",
        filename: "Dunes.jpg",
        kind: "IMAGE",
        thumbnail_url: "/media/dunes",
        fields: ["checksum"],
        captured_at: "2026-07-01T13:00:00",
        new_captured_at: "2026-07-01T13:00:00",
        available: true,
        new_available: true,
        other_albums: [{ id: "album-2", title: "Family" }],
      },
    ],
    cover_choices: [
      {
        moment_id: "day-1",
        moment_label: "First day",
        entry_id: cover?.entry_id ?? "",
        options: [
          {
            entry_id: "dunes",
            filename: "Dunes.jpg",
            thumbnail_url: "/media/dunes",
          },
          {
            entry_id: "source:asset-new",
            filename: "Sunset.jpg",
            thumbnail_url: "/api/media/sources/assets/asset-new/thumbnail",
          },
        ],
      },
    ],
    moments: [
      { id: "day-1", label: "First day", new: false },
      { id: "new:2026-07-02", label: "July 2, 2026", new: true },
    ],
    audience: exclude
      ? [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: [],
            lost_entry_ids: ["beach"],
          },
        ]
      : [
          {
            person_id: "alex",
            display_name: "Alex",
            gained_entry_ids: ["source:asset-new"],
            lost_entry_ids: ["beach"],
          },
        ],
    blockers,
  };
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
      return Promise.resolve(handler(path, options));
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("reports an album that already matches Immich", async () => {
  mockAPI((path) =>
    path.endsWith("/sync/check")
      ? Response.json(emptyReview)
      : Response.json(album),
  );
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Check for changes" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Changes in Immich",
  });
  expect(
    await within(dialog).findByText(
      "This album matches Immich. Nothing to apply.",
    ),
  ).toBeVisible();
  expect(
    within(dialog).getByRole("button", { name: "Apply changes" }),
  ).toBeDisabled();
  await user.click(within(dialog).getByRole("button", { name: "Close" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("reviews additions, removals, changes, and covers before applying the reviewed token", async () => {
  const checks: SyncRequest[] = [];
  let applied: SyncRequest | undefined;
  mockAPI((path, options) => {
    if (path.endsWith("/sync/check")) {
      const request = JSON.parse(String(options?.body)) as SyncRequest;
      checks.push(request);
      return Response.json(reviewFor(request));
    }
    if (path.endsWith("/sync/apply")) {
      applied = JSON.parse(String(options?.body)) as SyncRequest;
      return Response.json(album);
    }
    return Response.json(album);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Check for changes" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Changes in Immich",
  });
  const additions = await within(dialog).findByRole("region", {
    name: "New in Immich",
  });
  expect(within(additions).getByText("Sunset.jpg")).toBeVisible();
  const destination = within(additions).getByRole("combobox", {
    name: "Destination for Sunset.jpg",
  });
  expect(destination).toHaveTextContent("First day");
  const removals = within(dialog).getByRole("region", {
    name: "Removed in Immich",
  });
  expect(within(removals).getByText(/Beach.jpg/)).toBeVisible();
  expect(within(removals).getByText(/was the cover/)).toBeVisible();
  const changes = within(dialog).getByRole("region", {
    name: "Changed in Immich",
  });
  expect(within(changes).getByText("new file version")).toBeVisible();
  expect(within(changes).getByText("Also updates in Family")).toBeVisible();
  expect(
    within(dialog).getByRole("region", { name: "Description change" }),
  ).toHaveTextContent("A week by the sea");
  const visibility = within(dialog).getByRole("region", {
    name: "Visibility review",
  });
  expect(within(visibility).getByText(/gains 1/)).toBeVisible();
  expect(within(visibility).getByText(/loses 1/)).toBeVisible();
  const apply = within(dialog).getByRole("button", { name: "Apply changes" });
  expect(apply).toBeDisabled();
  expect(within(dialog).getByRole("alert")).toHaveTextContent(
    "Choose a new cover for First day.",
  );

  // Choosing the replacement cover re-reviews with that choice and unblocks.
  await user.click(
    within(removals).getByRole("radio", {
      name: "Use Dunes.jpg as the cover of First day",
    }),
  );
  await waitFor(() => expect(checks).toHaveLength(2));
  expect(checks[1]?.covers).toEqual([
    { moment_id: "day-1", entry_id: "dunes" },
  ]);
  await waitFor(() => expect(apply).toBeEnabled());

  // Keeping the new item out is a placement too, and changes the audience.
  await user.click(destination);
  await user.click(
    screen.getByRole("option", { name: "Keep out of this album" }),
  );
  await waitFor(() => expect(checks).toHaveLength(3));
  expect(checks[2]?.placements).toEqual([
    { source_id: "asset-new", moment_id: "", exclude: true },
  ]);
  expect(
    await within(dialog).findByRole("region", {
      name: "Kept out of this album",
    }),
  ).toBeVisible();
  expect(within(visibility).queryByText(/gains 1/)).not.toBeInTheDocument();

  await user.click(
    within(dialog).getByRole("button", { name: "Apply changes" }),
  );
  await waitFor(() =>
    expect(applied).toEqual({
      placements: [{ source_id: "asset-new", moment_id: "", exclude: true }],
      covers: [{ moment_id: "day-1", entry_id: "dunes" }],
      review_token: "token:day-1:true:dunes",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
});

it("shows a stale review and offers to check again", async () => {
  let checks = 0;
  mockAPI((path) => {
    if (path.endsWith("/sync/check")) {
      checks++;
      return Response.json(
        reviewFor({
          placements: [],
          covers: [{ moment_id: "day-1", entry_id: "dunes" }],
          review_token: "",
        }),
      );
    }
    if (path.endsWith("/sync/apply"))
      return Response.json(
        {
          error: {
            code: "sync_changed",
            message:
              "Immich or this Album changed after this review. Check for changes again before applying.",
          },
        },
        { status: 409 },
      );
    return Response.json(album);
  });
  window.history.replaceState(null, "", "/curator/albums/album-1");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Check for changes" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Changes in Immich",
  });
  await user.click(
    await within(dialog).findByRole("radio", {
      name: "Use Dunes.jpg as the cover of First day",
    }),
  );
  const apply = within(dialog).getByRole("button", { name: "Apply changes" });
  await waitFor(() => expect(apply).toBeEnabled());
  await user.click(apply);
  expect(
    await within(dialog).findByText(/changed after this review/),
  ).toBeVisible();
  expect(checks).toBe(1);
  await user.click(within(dialog).getByRole("button", { name: "Check again" }));
  await waitFor(() => expect(checks).toBe(2));
});
