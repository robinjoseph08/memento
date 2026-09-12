import { QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";

import { createQueryClient } from "../../lib/query-client";
import type {
  AccessPerson,
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
  excluded_count: 0,
}));
const album: ViewerAlbum = {
  id: "lake",
  title: "Lake weekend",
  description: "",
  photo_count: 2,
  video_count: 0,
  start_date: "2025-06-14",
  end_date: "2025-06-14",
  cover_url: "/preview/jamie/cover",
  days: [{ date: "2025-06-14", photo_count: 2, video_count: 0 }],
};
const photo: ViewerEntry = {
  id: "one",
  kind: "IMAGE",
  title: "Jamie's photo",
  captured_at: "2025-06-14T12:00:00Z",
  available: true,
  thumbnail_url: "/preview/jamie/one",
  width: 1200,
  height: 800,
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
          person: { id: "curator", display_name: "Robin", is_curator: true },
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
          element: <ViewerPreview albumID="lake" people={people} />,
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
        entries: [{ ...photo, id: "two", title: "Jamie's second photo" }],
        next_cursor: "",
      });
    if (path === "/api/curator/albums/lake/preview/alex") return alex;
    if (path === "/api/curator/albums/lake/preview/alex/photos")
      return Response.json({
        entries: [
          {
            ...photo,
            title: "Alex's photo",
            thumbnail_url: "/preview/alex/one",
          },
        ],
        next_cursor: "",
      });
    if (path === "/api/albums/lake")
      return Response.json({
        ...album,
        cover_url: "/member/cover",
        photo_count: 1,
      });
    if (path === "/api/albums/lake/photos")
      return Response.json({
        entries: [
          { ...photo, title: "Member photo", thumbnail_url: "/member/one" },
        ],
        next_cursor: "",
      });
    throw new Error(`Unexpected request: ${path}`);
  });
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: "Load more photos" }),
  );
  expect(
    await screen.findByRole("img", { name: "Jamie's second photo" }),
  ).toBeVisible();
  expect(screen.getByText("Previewing as Jamie")).toBeVisible();
  expect(screen.getByText(/Read-only preview/)).toBeVisible();
  await user.click(screen.getByRole("combobox", { name: "Preview as person" }));
  await user.type(screen.getByPlaceholderText("Search people…"), "Alex");
  await user.click(screen.getByRole("option", { name: "Alex" }));
  expect(new URLSearchParams(window.location.search).get("person")).toBe(
    "alex",
  );
  expect(
    screen.queryByRole("img", { name: "Jamie's photo" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", { name: "Jamie's second photo" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
  await act(async () =>
    resolveAlex(
      Response.json({
        ...album,
        photo_count: 1,
        cover_url: "/preview/alex/cover",
        days: [{ date: "2025-06-14", photo_count: 1, video_count: 0 }],
      }),
    ),
  );
  expect(
    await screen.findByRole("img", { name: "Alex's photo" }),
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
    await screen.findByRole("img", { name: "Member photo" }),
  ).toHaveAttribute("src", "/member/one");
  expect(screen.queryByText(/Previewing as/)).not.toBeInTheDocument();
  expect(
    screen.queryByRole("img", { name: "Alex's photo" }),
  ).not.toBeInTheDocument();
});

it("keeps the access editor pane open when leaving a mobile preview", async () => {
  window.history.replaceState(
    null,
    "",
    "/curator/albums/lake?section=preview&pane=detail",
  );
  renderPreview(() => Response.json(album));
  const user = userEvent.setup();
  await screen.findByRole("button", { name: "Account menu" });
  await user.click(screen.getByRole("link", { name: "Edit album access" }));
  const params = new URLSearchParams(window.location.search);
  expect(params.get("section")).toBe("access");
  expect(params.get("pane")).toBe("detail");
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
  ).toHaveAttribute("aria-disabled", "true");
  expect(
    screen.getByRole("menuitem", { name: "Immich connection" }),
  ).toHaveAttribute("aria-disabled", "true");
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
  expect(screen.getByText("Previewing as Alex")).toBeVisible();
  expect(
    screen.getByText("This person has no access to this album."),
  ).toBeVisible();
  expect(
    screen.queryByRole("img", { name: "Album cover" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /Download/i }),
  ).not.toBeInTheDocument();
});
