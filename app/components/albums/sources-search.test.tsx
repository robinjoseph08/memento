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

function mockSources() {
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
      if (path.endsWith("/albums")) return Response.json([]);
      const params = new URL(path, "http://localhost").searchParams;
      const page = Number(params.get("page") ?? 1);
      return Response.json({
        albums: [
          {
            id: "summer",
            title: params.get("q") ? "Summer by the sea" : "Family albums",
            count: 36,
            start_date: "",
            end_date: "",
            cover_url: "",
            album_id: "",
          },
        ],
        page,
        pages: 3,
        total: 3,
      });
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("returns from Immich sources to All albums", async () => {
  mockSources();
  window.history.replaceState(null, "", "/curator/import");
  const user = userEvent.setup();
  render(<App />);
  const back = await screen.findByRole("link", { name: "All albums" });
  expect(back).toHaveAttribute("href", "/curator");
  await user.click(back);
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
});

it("searches and clears Immich albums without replacing the focused input, resetting pagination", async () => {
  mockSources();
  window.history.replaceState(null, "", "/curator/import?page=2");
  const user = userEvent.setup();
  render(<App />);
  expect(await screen.findByText("Page 2 of 3")).toBeVisible();
  const form = screen.getByRole("search", { name: "Search Immich albums" });
  const input = within(form).getByRole("searchbox", {
    name: "Search Immich albums",
  });
  await user.type(input, "Summer{Enter}");
  await waitFor(() => expect(window.location.search).toBe("?q=Summer&page=1"));
  expect(await screen.findByText("Page 1 of 3")).toBeVisible();
  expect(
    await screen.findByRole("heading", { name: "Summer by the sea" }),
  ).toBeVisible();
  expect(input).toHaveFocus();
  await user.type(input, " trip");
  await user.click(within(form).getByRole("button", { name: "Search" }));
  await waitFor(() =>
    expect(window.location.search).toBe("?q=Summer+trip&page=1"),
  );
  expect(input).toHaveFocus();
  await user.click(screen.getByRole("link", { name: "Next page" }));
  expect(await screen.findByText("Page 2 of 3")).toBeVisible();
  await user.type(input, " draft");
  await user.click(within(form).getByRole("button", { name: "Clear search" }));
  await waitFor(() => {
    expect(new URLSearchParams(window.location.search).get("q") ?? "").toBe("");
    expect(new URLSearchParams(window.location.search).get("page")).toBe("1");
  });
  expect(
    await screen.findByRole("heading", { name: "Family albums" }),
  ).toBeVisible();
  expect(input).toHaveValue("");
  expect(input).toHaveFocus();
  expect(
    within(form).queryByRole("button", { name: "Clear search" }),
  ).not.toBeInTheDocument();
});
