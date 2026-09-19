import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";

import { createQueryClient } from "../../lib/query-client";
import type {
  AccessPerson,
  AlbumDetail,
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";
import { AppShell } from "../pages/layouts";
import { ViewerAlbumPage } from "../pages/viewer";
import { ViewerPreview } from "./viewer-preview";

const people: AccessPerson[] = ["Jamie", "Alex"].map((name) => ({
  person_id: name.toLowerCase(),
  display_name: name,
  avatar_url: "",
  decision: "inherit",
  detected: false,
  suggested: false,
  supporting_entries: 0,
  inherited: false,
  effective: false,
  accessible_count: 0,
  exceptions: 0,
  moments_detected: 0,
  deactivated: false,
}));
const frozen: AccessPerson = {
  ...people[0],
  person_id: "lee",
  display_name: "Lee",
  deactivated: true,
};
const curatorAlbum: AlbumDetail = {
  id: "lake",
  source_id: "source",
  title: "Lake weekend",
  description: "",
  published: false,
  ready: false,
  status: "complete",
  message: "",
  processed: 2,
  total: 2,
  photo_count: 2,
  video_count: 0,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "",
  moments: [],
  access: [...people, frozen],
  excluded: [],
};
const album: ViewerAlbum = {
  id: "lake",
  title: "Lake weekend",
  description: "",
  photo_count: 2,
  video_count: 0,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "/preview/jamie/cover/thumb",
  cover_preview_url: "/preview/jamie/cover",
  days: [
    {
      date: "2025-06-14",
      photo_count: 2,
      video_count: 0,
      photo_ratios: [1.5, 1.5],
      video_ratios: [],
    },
  ],
};
const photo: ViewerEntry = {
  id: "one",
  kind: "IMAGE",
  title: "Jamie's photo",
  captured_at: "2025-06-14T12:00:00Z",
  available: true,
  thumbnail_url: "/preview/jamie/one/thumb",
  preview_url: "/preview/jamie/one",
  download_url: "",
  playback_url: "",
  width: 1200,
  height: 800,
  chapters: [],
  chapter_status: "",
};
function renderPreview(
  handler: (path: string) => Response | Promise<Response>,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: {
            id: "curator",
            display_name: "Robin",
            is_curator: true,
            onboarding_completed_at: "2026-01-01T00:00:00Z",
          },
          auth_mode: "fake",
        });
      return handler(path);
    }),
  );
  const router = createBrowserRouter([
    {
      element: <AppShell />,
      children: [
        {
          path: "/curator/albums/:id",
          element: <ViewerPreview album={curatorAlbum} />,
        },
        {
          path: "/albums/:id/photos",
          element: <ViewerAlbumPage tab="photos" />,
        },
      ],
    },
  ]);
  render(
    <QueryClientProvider client={createQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("switches the URL identity without retaining another person's cover, counts, or cursor pages", async () => {
  let resolveAlex!: (response: Response) => void;
  const alex = new Promise<Response>((resolve) => {
    resolveAlex = resolve;
  });
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&person=jamie&tab=photos",
  );
  const router = renderPreview((path) => {
    if (path === "/api/curator/albums/lake/preview/jamie")
      return Response.json(album);
    if (path === "/api/curator/albums/lake/preview/jamie/photos")
      return Response.json({ entries: [photo], next_cursor: "jamie-next" });
    if (path.endsWith("/jamie/photos?cursor=jamie-next"))
      return Response.json({
        entries: [
          {
            ...photo,
            id: "two",
            title: "Jamie's second photo",
            captured_at: "2025-06-14T13:00:00Z",
          },
        ],
        next_cursor: "",
      });
    if (path === "/api/curator/albums/lake/preview/alex") return alex;
    if (path === "/api/curator/albums/lake/preview/alex/photos")
      return Response.json({
        entries: [
          {
            ...photo,
            title: "Alex's photo",
            captured_at: "2025-06-14T14:00:00Z",
            preview_url: "/preview/alex/one",
          },
        ],
        next_cursor: "",
      });
    if (path === "/api/albums/lake")
      return Response.json({
        ...album,
        cover_preview_url: "/member/cover",
        photo_count: 1,
      });
    if (path === "/api/albums/lake/photos")
      return Response.json({
        entries: [
          {
            ...photo,
            title: "Member photo",
            captured_at: "2025-06-14T15:00:00Z",
            preview_url: "/member/one",
          },
        ],
        next_cursor: "",
      });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  expect(
    await screen.findByRole("img", {
      name: "Photo taken June 14, 2025 at 1:00 PM",
    }),
  ).toBeVisible();
  expect(
    screen.getByRole("combobox", { name: "Preview as" }),
  ).toHaveTextContent("Jamie");
  expect(screen.getByText(/Showing the view after publication/)).toBeVisible();
  expect(document.querySelector('[aria-label="Timeline"]')).toBeNull();
  await user.click(screen.getByRole("combobox", { name: "Preview as" }));
  expect(screen.queryByRole("option", { name: "Lee" })).not.toBeInTheDocument();
  await user.type(screen.getByPlaceholderText("Search people…"), "Alex");
  await user.click(screen.getByRole("option", { name: "Alex" }));
  expect(new URLSearchParams(window.location.search).get("person")).toBe(
    "alex",
  );
  expect(
    screen.queryByRole("img", {
      name: "Photo taken June 14, 2025 at 12:00 PM",
    }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", {
      name: "Photo taken June 14, 2025 at 1:00 PM",
    }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
  await act(async () =>
    resolveAlex(
      Response.json({
        ...album,
        photo_count: 1,
        cover_preview_url: "/preview/alex/cover",
        days: [
          {
            date: "2025-06-14",
            photo_count: 1,
            video_count: 0,
            photo_ratios: [1.5],
            video_ratios: [],
          },
        ],
      }),
    ),
  );
  expect(
    await screen.findByRole("img", {
      name: "Photo taken June 14, 2025 at 2:00 PM",
    }),
  ).toHaveAttribute("src", "/preview/alex/one");
  expect(screen.getByRole("link", { name: "Photos 1" })).toBeVisible();
  expect(screen.getByRole("img", { name: "Album cover" })).toHaveAttribute(
    "src",
    "/preview/alex/cover",
  );
  await act(async () => {
    await router.navigate("/albums/lake/photos");
  });
  expect(
    await screen.findByRole("img", {
      name: "Photo taken June 14, 2025 at 3:00 PM",
    }),
  ).toHaveAttribute("src", "/member/one");
  expect(
    screen.queryByRole("combobox", { name: "Preview as" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", {
      name: "Photo taken June 14, 2025 at 2:00 PM",
    }),
  ).not.toBeInTheDocument();
});

it("defaults to the first person when the URL names none", async () => {
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&pane=detail",
  );
  renderPreview((path) =>
    path.endsWith("/photos")
      ? Response.json({ entries: [], next_cursor: "" })
      : Response.json({ ...album, photo_count: 0, days: [] }),
  );
  expect(
    await screen.findByRole("combobox", { name: "Preview as" }),
  ).toHaveTextContent("Jamie");
  expect(
    await screen.findByText("No photos are shared with Jamie yet."),
  ).toBeVisible();
  expect(screen.queryByRole("link", { name: /View videos/ })).toBeNull();
});

it("disables shell account actions only in the Curator preview section", async () => {
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&person=jamie",
  );
  const router = renderPreview((path) =>
    path.endsWith("/photos")
      ? Response.json({ entries: [], next_cursor: "" })
      : Response.json(album),
  );
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Account menu" }));
  expect(screen.getByRole("menuitem", { name: "Profile" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  expect(screen.getByRole("menuitem", { name: "Sign out" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  expect(
    screen.getByRole("menuitemcheckbox", { name: "Dark mode" }),
  ).not.toHaveAttribute("aria-disabled");
  expect(screen.getByRole("menuitem", { name: "Settings" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  await user.keyboard("{Escape}");
  await act(async () => {
    await router.navigate("/albums/lake/photos?section=preview&person=jamie");
  });
  await user.click(screen.getByRole("button", { name: "Account menu" }));
  expect(
    screen.getByRole("menuitem", { name: "Sign out" }),
  ).not.toHaveAttribute("aria-disabled");
  expect(screen.getByRole("menuitem", { name: "Profile" })).not.toHaveAttribute(
    "aria-disabled",
  );
});

it("keeps the selected identity and a neutral no-access message for denied or inactive people", async () => {
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&person=alex&tab=videos",
  );
  renderPreview(() =>
    Response.json({ error: { message: "Not found" } }, { status: 404 }),
  );
  expect(
    await screen.findByRole("heading", { name: "Album not available" }),
  ).toBeVisible();
  expect(
    screen.getByRole("combobox", { name: "Preview as" }),
  ).toHaveTextContent("Alex");
  expect(screen.getByText("Alex has no access to this album.")).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /Download/i }),
  ).not.toBeInTheDocument();
});

it("navigates photos inside the preview with the identity notice and no download", async () => {
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&person=jamie&tab=photos",
  );
  renderPreview((path) =>
    path.endsWith("/photos")
      ? Response.json({
          entries: [
            photo,
            {
              ...photo,
              id: "two",
              title: "Second",
              captured_at: "2025-06-14T13:00:00Z",
            },
          ],
          next_cursor: "",
        })
      : Response.json(album),
  );
  const user = userEvent.setup();
  const opener = await screen.findByRole("link", {
    name: "Open photo taken June 14, 2025 at 12:00 PM",
  });
  expect(opener).toHaveAttribute(
    "href",
    "/curator/albums/lake?section=preview&person=jamie&tab=photos&entry=one",
  );
  await user.click(opener);
  const dialog = await screen.findByRole("dialog", { name: "Photo 1 of 2" });
  expect(within(dialog).getByText(/Previewing as Jamie/)).toBeVisible();
  expect(
    within(dialog).queryByRole("link", { name: "Download photo" }),
  ).not.toBeInTheDocument();
  await user.keyboard("{ArrowRight}");
  expect(
    await screen.findByRole("dialog", { name: "Photo 2 of 2" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("entry")).toBe("two");
  await user.keyboard("{Escape}");
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(new URLSearchParams(window.location.search).get("entry")).toBeNull();
  await waitFor(() =>
    expect(
      screen.getByRole("link", {
        name: "Open photo taken June 14, 2025 at 12:00 PM",
      }),
    ).toHaveFocus(),
  );
});

it("offers neither Cast nor AirPlay while previewing a video", async () => {
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  // A browser that could cast: the preview still never loads the SDK.
  vi.stubGlobal("chrome", {});
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&person=jamie&tab=videos&entry=clip",
  );
  const clip: ViewerEntry = {
    ...photo,
    id: "clip",
    kind: "VIDEO",
    title: "Jamie's video",
    playback_url: "/preview/jamie/clip/playback",
  };
  renderPreview((path) =>
    path.endsWith("/videos")
      ? Response.json({ entries: [clip], next_cursor: "" })
      : Response.json({
          ...album,
          photo_count: 0,
          video_count: 1,
          days: [
            {
              date: "2025-06-14",
              photo_count: 0,
              video_count: 1,
              photo_ratios: [],
              video_ratios: [1.5],
            },
          ],
        }),
  );
  const dialog = await screen.findByRole("dialog", { name: "Video 1 of 1" });
  const video = within(dialog).getByLabelText("Jamie's video");
  expect(video).toHaveAttribute("x-webkit-airplay", "deny");
  act(() => {
    video.dispatchEvent(
      Object.assign(new Event("webkitplaybacktargetavailabilitychanged"), {
        availability: "available",
      }),
    );
  });
  expect(
    within(dialog).queryByRole("button", { name: "AirPlay" }),
  ).not.toBeInTheDocument();
  expect(document.querySelector('script[src*="cast_sender"]')).toBeNull();
  expect(
    within(dialog).queryByRole("button", { name: "Cast" }),
  ).not.toBeInTheDocument();
});
