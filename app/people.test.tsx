import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

const curator = { id: "curator", display_name: "Robin", is_curator: true };
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it.each([true, false])(
  "keeps Google's no-access explanation after a failed sign-in, claimed=%s",
  async (claimed) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path.endsWith("/status"))
          return Response.json({ claimed, auth_mode: "google" });
        return Response.json({ healthy: false });
      }),
    );
    window.history.replaceState(null, "", "/sign-in?error=access_denied");
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This Google account does not have access",
    );
    expect(
      screen.getByRole("link", { name: "Continue with Google" }),
    ).toHaveAttribute("href", "/api/identity/google/start");
  },
);

it("keeps the same people search input focused when submitting and clearing a search", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) =>
      Response.json(
        path.endsWith("/status")
          ? { claimed: true, person: curator, auth_mode: "fake" }
          : [],
      ),
    ),
  );
  window.history.replaceState(null, "", "/curator/people");
  const user = userEvent.setup();
  render(<App />);
  const search = await screen.findByRole("searchbox", {
    name: "Search people",
  });
  expect(document.title).toBe("People | Memento");
  await user.type(search, "alex{Enter}");
  await waitFor(() => expect(window.location.search).toBe("?q=alex"));
  expect(search).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "Search" }));
  expect(search).toHaveFocus();
  await user.type(search, " draft");
  await user.click(screen.getByRole("button", { name: "Clear search" }));
  await waitFor(() => expect(window.location.search).toBe(""));
  expect(search).toHaveValue("");
  expect(search).toHaveFocus();
  expect(
    screen.queryByRole("button", { name: "Clear search" }),
  ).not.toBeInTheDocument();
  await user.type(search, "unsubmitted");
  expect(window.location.search).toBe("");
  await user.click(screen.getByRole("button", { name: "Clear search" }));
  expect(search).toHaveValue("");
  expect(search).toHaveFocus();
});

it("preserves edits when the server rejects a Person change", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (options?.method === "POST")
        return Response.json(
          {
            error: {
              message: "This change could not be saved.",
            },
          },
          { status: 409 },
        );
      return Response.json({
        person: { ...curator, id: "other-curator" },
        identities: [],
        preauthorizations: [],
        sessions: [],
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/people/other-curator");
  const user = userEvent.setup();
  render(<App />);
  const role = await screen.findByRole("checkbox", { name: "Curator" });
  await user.click(role);
  await user.click(screen.getByRole("button", { name: "Save person" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "This change could not be saved.",
  );
  expect(role).not.toBeChecked();
  expect(window.location.pathname).toBe("/curator/people/other-curator");
});

it("keeps successfully saved Person values when the following refresh fails", async () => {
  const alex = { id: "alex", display_name: "Alex", is_curator: false };
  let saved = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (options?.method === "POST") {
        saved = true;
        return Response.json({
          ...alex,
          display_name: "Saved Alex",
          is_curator: true,
        });
      }
      if (saved)
        return Response.json(
          { error: { message: "Refresh unavailable." } },
          { status: 503 },
        );
      return Response.json({
        person: alex,
        identities: [],
        preauthorizations: [],
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/people/alex");
  const user = userEvent.setup();
  render(<App />);
  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.clear(name);
  await user.type(name, "Saved Alex");
  await user.click(screen.getByRole("checkbox", { name: "Curator" }));
  await user.click(screen.getByRole("button", { name: "Save person" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Person saved.");
  expect(screen.getByRole("alert")).toHaveTextContent("Something went wrong.");
  expect(name).toHaveValue("Saved Alex");
  expect(screen.getByRole("checkbox", { name: "Curator" })).toBeChecked();
});

it("creates a person without losing a rejected display name and opens their access details", async () => {
  let rejected = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path === "/api/people" && options?.method === "POST") {
        if (rejected)
          return Response.json(
            {
              error: {
                message: "Check the highlighted fields.",
                fields: { display_name: "Choose a display name." },
              },
            },
            { status: 400 },
          );
        return Response.json({
          id: "alex",
          display_name: "Alex",
          is_curator: false,
        });
      }
      if (path === "/api/people/alex")
        return Response.json({
          person: { id: "alex", display_name: "Alex", is_curator: false },
          identities: [],
          preauthorizations: [],
        });
      if (path.startsWith("/api/people")) return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/people");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Add person" }));
  const name = screen.getByRole("textbox", { name: "Display name" });
  await user.type(name, "Alex{Enter}");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Check the highlighted fields.",
  );
  expect(name).toHaveValue("Alex");
  expect(name).toHaveFocus();
  rejected = false;
  await user.click(screen.getByRole("button", { name: "Create person" }));
  expect(
    await screen.findByRole("heading", { name: "Alex" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Preauthorizations" }),
  ).toBeInTheDocument();
});
