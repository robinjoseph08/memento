import {
  act,
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
import type { Album, AlbumDetail } from "../../types/generated/publishing";

const album: Album = {
  id: "summer",
  source_id: "immich-summer",
  title: "Summer by the sea",
  description: "A week away",
  published: false,
  status: "complete",
  message: "",
  processed: 36,
  total: 36,
  photo_count: 30,
  video_count: 6,
  start_date: "2026-07-01T12:00:00Z",
  end_date: "2026-07-07T12:00:00Z",
  cover_url: "/api/curator/albums/summer/cover",
};

function mockAlbums(handler: (path: string) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: { id: "robin", display_name: "Robin", is_curator: true },
          auth_mode: "fake",
        });
      if (path.endsWith("/connection"))
        return Response.json({ usable: true, import_supported: true });
      return handler(path);
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("lists imported covers, separate photo and video counts, and capture dates", async () => {
  mockAlbums(() => Response.json([album]));
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  const link = await screen.findByRole("link", { name: /Summer by the sea/ });
  expect(within(link).getByRole("img", { name: album.title })).toHaveAttribute(
    "src",
    "/api/curator/albums/summer/cover",
  );
  expect(link).toHaveTextContent("30 photos, 6 videos");
  expect(link).toHaveTextContent("Jul 1, 2026 to Jul 7, 2026");
  expect(link).toHaveAttribute("href", "/curator/albums/summer");
});

it("distinguishes an empty collection from an empty search", async () => {
  mockAlbums(() => Response.json([]));
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  expect(
    screen.getByText(
      "Import an album from Immich to start organizing your photos and videos.",
    ),
  ).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "No matching albums" }),
  ).not.toBeInTheDocument();
});

it("uses neutral fallbacks for empty and broken covers without inventing capture dates", async () => {
  mockAlbums(() =>
    Response.json([
      album,
      {
        ...album,
        id: "no-cover",
        title: "A single photo",
        photo_count: 1,
        video_count: 0,
        start_date: "",
        end_date: "",
        cover_url: "",
      },
    ]),
  );
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  const noCover = await screen.findByRole("link", { name: /A single photo/ });
  expect(within(noCover).getByText("No cover available")).toBeVisible();
  expect(within(noCover).queryByRole("img")).not.toBeInTheDocument();
  expect(noCover).toHaveTextContent("1 photo, 0 videos");
  expect(noCover).not.toHaveTextContent("2026");
  fireEvent.error(screen.getByRole("img", { name: album.title }));
  const brokenCover = screen.getByRole("link", { name: /Summer by the sea/ });
  expect(within(brokenCover).getByText("No cover available")).toBeVisible();
  expect(within(brokenCover).queryByRole("img")).not.toBeInTheDocument();
});

it.each([
  ["queued", "Waiting to import"],
  ["processing", "Import in progress"],
  ["interrupted", "Import interrupted"],
  ["failed", "Import failed"],
])(
  "shows %s honestly without partial gallery content",
  async (status, label) => {
    const pending: AlbumDetail = {
      ...album,
      status,
      photo_count: 0,
      video_count: 0,
      cover_url: "",
      moments: [
        {
          id: "partial-day",
          title: "Partial day",
          label: "Partial day",
          date: "2026-07-01",
          end_date: "2026-07-01",
          cover_entry_id: "partial-photo",
          access: { people: [], faces: [], refresh_error: "" },
          entries: [
            {
              id: "partial-photo",
              media_id: "photo",
              filename: "Partial.jpg",
              kind: "IMAGE",
              captured_at: "2026-07-01T12:00:00Z",
              available: true,
              thumbnail_url: "/media/partial-photo",
            },
          ],
        },
      ],
    };
    mockAlbums((path) =>
      Response.json(path === "/api/curator/albums" ? [pending] : pending),
    );
    window.history.replaceState(null, "", "/curator");
    const user = userEvent.setup();
    render(<App />);
    const link = await screen.findByRole("link", { name: /Summer by the sea/ });
    expect(link).toHaveTextContent(label);
    expect(link).not.toHaveTextContent("0 photos");
    expect(link).not.toHaveTextContent("0 videos");
    expect(link).not.toHaveTextContent("Jul 1");
    expect(within(link).getByText("No cover available")).toBeVisible();
    await user.click(link);
    expect(
      await screen.findByRole("heading", { name: album.title }),
    ).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "Moments" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("list", { name: "Moment media" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("img", { name: "Partial.jpg" }),
    ).not.toBeInTheDocument();
  },
);

it("submits literal album searches, clears immediately with focus, and restores search through browser history", async () => {
  mockAlbums((path) => {
    if (path === "/api/curator/albums") return Response.json([album]);
    if (path === "/api/curator/albums?q=Beach%20%26%20sea%25")
      return Response.json([]);
    throw new Error(`Unexpected request: ${path}`);
  });
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("link", { name: /Summer by the sea/ });
  const form = screen.getByRole("search", { name: "Search albums" });
  const input = within(form).getByRole("searchbox", { name: "Search albums" });
  await user.type(input, "  Beach & sea%  ");
  expect(window.location.search).toBe("");
  await user.keyboard("{Enter}");
  expect(
    await screen.findByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  expect(screen.getByText("Try another search.")).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "No albums yet" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /Summer by the sea/ }),
  ).not.toBeInTheDocument();
  expect(new URLSearchParams(window.location.search).get("q")).toBe(
    "Beach & sea%",
  );
  expect(input).toHaveValue("Beach & sea%");
  expect(input).toHaveFocus();
  await user.type(input, " draft");
  await user.click(within(form).getByRole("button", { name: "Clear search" }));
  expect(input).toHaveValue("");
  expect(input).toHaveFocus();
  expect(new URLSearchParams(window.location.search).get("q")).toBeNull();
  expect(
    await screen.findByRole("link", { name: /Summer by the sea/ }),
  ).toBeVisible();
  expect(
    within(form).queryByRole("button", { name: "Clear search" }),
  ).not.toBeInTheDocument();
  await act(async () => window.history.back());
  await waitFor(() => expect(input).toHaveValue("Beach & sea%"));
  expect(
    await screen.findByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  await act(async () => window.history.forward());
  await waitFor(() => expect(input).toHaveValue(""));
  expect(
    await screen.findByRole("link", { name: /Summer by the sea/ }),
  ).toBeVisible();
});
