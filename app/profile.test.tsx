import { focusManager } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

const alex = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  update_email: "alex@example.test",
  email_updates: false,
};
const robin = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  update_email: "private@example.test",
  email_updates: false,
};
const account = {
  id: "google-alex",
  provider: "google",
  email: "alex@example.test",
  created_at: "2026-01-01T00:00:00Z",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  focusManager.setFocused(undefined);
  window.history.replaceState(null, "", "/");
});

it("keeps profile edits through failed refresh, focuses a rejected email, and saves linked-address preferences", async () => {
  let failRead = false;
  let failSave = true;
  let profile = { person: alex, identities: [account] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: alex,
          auth_mode: "fake",
        });
      if (path.endsWith("/sessions")) return Response.json([]);
      if (options?.method === "POST") {
        if (failSave)
          return Response.json(
            {
              error: {
                fields: { update_email: "Choose a linked email address." },
              },
            },
            { status: 400 },
          );
        profile = {
          ...profile,
          person: { ...alex, ...JSON.parse(String(options.body)) },
        };
      } else if (failRead) return Response.json({}, { status: 503 });
      return Response.json(profile);
    }),
  );
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.clear(name);
  await user.type(name, "Alex edited");
  failRead = true;
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  expect(name).toHaveValue("Alex edited");
  failRead = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  const email = screen.getByRole("combobox", { name: "Email for updates" });
  expect(
    await screen.findByText("Choose a linked email address."),
  ).toBeVisible();
  expect(email).toHaveFocus();
  expect(screen.getAllByRole("option")).toHaveLength(2);
  failSave = false;
  await user.click(
    screen.getByRole("checkbox", { name: "Email me when there are updates" }),
  );
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Profile saved.");
  expect(name).toHaveValue("Alex edited");
});

it("replaces a private profile on identity refresh and ignores the old in-flight session response", async () => {
  let person = robin;
  let release!: (response: Response) => void;
  let oldSignal: AbortSignal | null | undefined;
  const held = new Promise<Response>((resolve) => {
    release = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/profile"))
        return Response.json({
          person,
          identities: [{ ...account, email: person.update_email }],
        });
      if (path.endsWith("/sessions")) {
        if (person.id === robin.id) {
          oldSignal = options?.signal;
          return held;
        }
        return Response.json([]);
      }
      throw new Error(path);
    }),
  );
  window.history.replaceState(null, "", "/profile");
  render(<App />);
  expect(
    await screen.findByRole("textbox", { name: "Display name" }),
  ).toHaveValue("Robin");
  person = alex;
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Display name" })).toHaveValue(
      "Alex",
    ),
  );
  expect(screen.queryByText("private@example.test")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "People" }),
  ).not.toBeInTheDocument();
  expect(oldSignal?.aborted).toBe(true);
  await act(async () =>
    release(
      Response.json([
        {
          id: "old",
          email: "private@example.test",
          device: "Private device",
          current: true,
          created_at: "2026-01-01",
          last_used_at: "2026-01-01",
          expires_at: "2026-02-01",
        },
      ]),
    ),
  );
  expect(screen.queryByText("Private device")).not.toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Display name" })).toHaveValue(
    "Alex",
  );
});

it("removes a revoked session's private screen on a 401 and refreshes sign-in status", async () => {
  let signedIn = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: signedIn ? alex : null,
          auth_mode: "fake",
        });
      if (path.endsWith("/sessions")) return Response.json([]);
      if (options?.method === "POST") {
        signedIn = false;
        return Response.json(
          { error: { message: "Sign in again." } },
          { status: 401 },
        );
      }
      return Response.json({ person: alex, identities: [account] });
    }),
  );
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Save profile" }));
  expect(
    await screen.findByRole("heading", { name: "Welcome back" }),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("heading", { name: "Your profile" }),
  ).not.toBeInTheDocument();
});

it("guards unsaved profile changes when leaving or signing out", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: alex,
          auth_mode: "fake",
        });
      if (path.endsWith("/sessions")) return Response.json([]);
      return Response.json({ person: alex, identities: [account] });
    }),
  );
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.type(name, " edited");
  await user.click(screen.getByRole("link", { name: "Albums" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(name).toHaveValue("Alex edited");
  await user.click(screen.getByRole("button", { name: "Account menu" }));
  await user.click(screen.getByRole("menuitem", { name: "Sign out" }));
  expect(confirm).toHaveBeenCalledTimes(2);
  expect(name).toHaveValue("Alex edited");
  expect(window.location.pathname).toBe("/profile");
});

it("offers full-navigation Google sign-in and explains an account without access", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({ claimed: true, person: null, auth_mode: "google" }),
    ),
  );
  window.history.replaceState(null, "", "/sign-in?error=no_access");
  render(<App />);
  expect(
    await screen.findByRole("link", { name: "Continue with Google" }),
  ).toHaveAttribute("href", "/api/identity/google/start");
  expect(screen.getByRole("alert")).toHaveTextContent(
    "exact Google email address",
  );
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});
