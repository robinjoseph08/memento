import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";

// A tiny Immich with two albums and a server-side ignored set, so the page
// sees real list changes after each action instead of a canned response.
function mockSources() {
  const albums = [
    { id: "recents", title: "Recents" },
    { id: "summer", title: "Summer by the sea" },
  ];
  const ignored = new Set<string>();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: {
            id: "robin",
            display_name: "Robin",
            is_curator: true,
            onboarding_completed_at: "2026-01-01T00:00:00Z",
          },
          auth_mode: "fake",
        });
      if (path.endsWith("/connection"))
        return Response.json({ usable: true, import_supported: true });
      if (
        path.endsWith("/sources/ignore") ||
        path.endsWith("/sources/restore")
      ) {
        const body = JSON.parse(String(init?.body)) as { source_id: string };
        if (path.endsWith("/sources/ignore")) ignored.add(body.source_id);
        else ignored.delete(body.source_id);
        return Response.json({});
      }
      const params = new URL(path, "http://localhost").searchParams;
      const listing = params.get("ignored") === "true";
      return Response.json({
        albums: albums
          .filter((album) => ignored.has(album.id) === listing)
          .map((album) => ({
            ...album,
            count: 3,
            start_date: "",
            end_date: "",
            cover_url: "",
            album_id: "",
          })),
        page: 1,
        pages: 1,
        total: 1,
        ignored: ignored.size,
      });
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("ignores an Immich album, lists it apart, and restores it", async () => {
  mockSources();
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByRole("heading", { name: "Recents" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: /ignored album/ }),
  ).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Ignore Recents" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Recents" }),
    ).not.toBeInTheDocument(),
  );
  expect(
    screen.getByRole("heading", { name: "Summer by the sea" }),
  ).toBeVisible();
  expect(screen.getByRole("link", { name: "1 ignored album" })).toBeVisible();

  await user.click(
    screen.getByRole("button", { name: "Ignore Summer by the sea" }),
  );
  expect(
    await screen.findByRole("heading", {
      name: "Every Immich album is ignored",
    }),
  ).toBeVisible();

  await user.click(screen.getByRole("link", { name: "2 ignored albums" }));
  expect(
    await screen.findByRole("heading", { name: "Ignored albums" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/curator/import/ignored");
  expect(await screen.findByRole("heading", { name: "Recents" })).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Summer by the sea" }),
  ).toBeVisible();

  await user.click(screen.getByRole("button", { name: "Restore Recents" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Recents" }),
    ).not.toBeInTheDocument(),
  );
  await user.click(
    screen.getByRole("button", { name: "Restore Summer by the sea" }),
  );
  expect(
    await screen.findByRole("heading", { name: "No ignored albums" }),
  ).toBeVisible();

  await user.click(screen.getByRole("link", { name: "Import an album" }));
  expect(await screen.findByRole("heading", { name: "Recents" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: /ignored album/ }),
  ).not.toBeInTheDocument();
});
