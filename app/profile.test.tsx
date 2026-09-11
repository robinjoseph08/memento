import { focusManager } from "@tanstack/react-query";
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";
import type { UpdateProfileRequest } from "./types/generated/identity";

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
  expect(document.title).toBe("Your profile | Memento");
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
  await user.click(email);
  const choices = screen.getByRole("listbox");
  expect(within(choices).getAllByRole("option")).toHaveLength(2);
  await user.click(
    within(choices).getByRole("option", { name: "alex@example.test" }),
  );
  expect(email).toHaveFocus();
  failSave = false;
  await user.click(
    screen.getByRole("checkbox", { name: "Email me when there are updates" }),
  );
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Profile saved.");
  expect(name).toHaveValue("Alex edited");
});

it("chooses a linked update email and clears it through the profile menu", async () => {
  let person = alex;
  const saved: UpdateProfileRequest[] = [];
  const identities = [
    account,
    { ...account, id: "second", email: "alex.second@example.test" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/sessions")) return Response.json([]);
      if (options?.method === "POST") {
        const request = JSON.parse(
          String(options.body),
        ) as UpdateProfileRequest;
        saved.push(request);
        person = { ...person, ...request };
      }
      return Response.json({ person, identities });
    }),
  );
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const email = await screen.findByRole("combobox", {
    name: "Email for updates",
  });
  await user.click(email);
  await user.click(
    screen.getByRole("option", { name: "alex.second@example.test" }),
  );
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Profile saved.");
  expect(saved[0]).toEqual({
    display_name: "Alex",
    update_email: "alex.second@example.test",
    email_updates: false,
  });
  expect(email).toHaveTextContent("alex.second@example.test");
  await user.click(email);
  await user.keyboard("{Home}{Enter}");
  expect(email).toHaveTextContent("No email selected");
  expect(email).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(saved).toHaveLength(2));
  expect(saved[1]).toEqual({
    display_name: "Alex",
    update_email: "",
    email_updates: false,
  });
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

function serveProfile() {
  let signedOut = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json(
          signedOut
            ? { claimed: true, auth_mode: "fake" }
            : { claimed: true, person: alex, auth_mode: "fake" },
        );
      if (path.endsWith("/sign-out")) {
        signedOut = true;
        return Response.json({});
      }
      if (path.endsWith("/sessions")) return Response.json([]);
      return Response.json({ person: alex, identities: [account] });
    }),
  );
}

it("guards unsaved profile changes when leaving or signing out", async () => {
  serveProfile();
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.type(name, " edited");
  const albums = screen.getByRole("link", { name: "Albums" });
  await user.click(albums);
  await user.click(
    within(
      await screen.findByRole("dialog", { name: "Leave this page?" }),
    ).getByRole("button", { name: "Cancel" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(albums).toHaveFocus();
  expect(name).toHaveValue("Alex edited");
  expect(window.location.pathname).toBe("/profile");
  const menuTrigger = screen.getByRole("button", { name: "Account menu" });
  await user.click(menuTrigger);
  await user.click(screen.getByRole("menuitem", { name: "Sign out" }));
  await user.click(
    within(await screen.findByRole("dialog", { name: "Sign out?" })).getByRole(
      "button",
      { name: "Cancel" },
    ),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(menuTrigger).toHaveFocus();
  expect(name).toHaveValue("Alex edited");
  expect(window.location.pathname).toBe("/profile");
  await user.click(menuTrigger);
  await user.click(screen.getByRole("menuitem", { name: "Sign out" }));
  await user.click(
    within(await screen.findByRole("dialog", { name: "Sign out?" })).getByRole(
      "button",
      { name: "Sign out" },
    ),
  );
  expect(
    await screen.findByRole("heading", { name: "Welcome back" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("leaves the profile once the person confirms losing their edits", async () => {
  serveProfile();
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  await user.type(
    await screen.findByRole("textbox", { name: "Display name" }),
    " edited",
  );
  await user.click(screen.getByRole("link", { name: "Albums" }));
  await user.click(
    within(
      await screen.findByRole("dialog", { name: "Leave this page?" }),
    ).getByRole("button", { name: "Leave page" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Your albums" }),
  ).toBeInTheDocument();
  expect(window.location.pathname).toBe("/albums");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it.each([false, true])(
  "protects the last linked account in your profile, Curator=%s",
  async (isCurator) => {
    const person = { ...alex, is_curator: isCurator };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path.endsWith("/status"))
          return Response.json({ claimed: true, person, auth_mode: "google" });
        if (path.endsWith("/sessions")) return Response.json([]);
        return Response.json({ person, identities: [account] });
      }),
    );
    window.history.replaceState(null, "", "/profile");
    render(<App />);
    const accounts = await screen.findByRole("table", {
      name: "Linked accounts",
    });
    expect(
      within(accounts).queryByRole("columnheader", { name: "Provider" }),
    ).not.toBeInTheDocument();
    expect(within(accounts).queryByText("Google")).not.toBeInTheDocument();
    expect(
      within(accounts).getByRole("button", {
        name: "Unlink alex@example.test",
      }),
    ).toBeDisabled();
    expect(
      screen.getByText("Keep at least one linked account so you can sign in."),
    ).toBeVisible();
  },
);

it.each([false, true])(
  "shows session expiration only to a Curator viewer, Curator=%s",
  async (isCurator) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path.endsWith("/status"))
          return Response.json({
            claimed: true,
            person: { ...alex, is_curator: isCurator },
            auth_mode: "fake",
          });
        if (path.endsWith("/sessions"))
          return Response.json([
            {
              id: "browser",
              identity_id: account.id,
              email: account.email,
              device: "Firefox on Mac",
              current: true,
              created_at: "2026-01-01T00:00:00Z",
              last_used_at: "2026-01-02T00:00:00Z",
              expires_at: "2026-02-01T00:00:00Z",
            },
          ]);
        return Response.json({ person: alex, identities: [account] });
      }),
    );
    window.history.replaceState(null, "", "/profile");
    render(<App />);
    const sessions = await screen.findByRole("table", {
      name: "Browser sessions",
    });
    const currentBrowser = within(sessions).getByRole("img", {
      name: "This browser",
    });
    expect(currentBrowser).toBeVisible();
    const user = userEvent.setup({ skipHover: true });
    await user.hover(currentBrowser);
    expect(
      await screen.findByRole("tooltip", { name: "This browser" }),
    ).toBeVisible();
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("tooltip")).not.toBeInTheDocument(),
    );
    expect(
      within(sessions).queryByRole("button", { name: "This browser" }),
    ).not.toBeInTheDocument();
    expect(within(sessions).getByText("Firefox on Mac")).toBeVisible();
    expect(within(sessions).getByText(account.email)).toBeVisible();
    expect(
      within(sessions).getByRole("columnheader", { name: "Signed in" }),
    ).toBeVisible();
    expect(
      within(sessions).getByRole("columnheader", { name: "Last used" }),
    ).toBeVisible();
    if (isCurator)
      expect(
        within(sessions).getByRole("columnheader", { name: "Expires" }),
      ).toBeVisible();
    else
      expect(
        within(sessions).queryByRole("columnheader", { name: "Expires" }),
      ).not.toBeInTheDocument();
  },
);

it("uses server notification defaults and preserves an unsaved opt-out on refresh", async () => {
  let person = { ...alex, email_updates: true };
  let identities = [account];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "google" });
      if (path.endsWith("/sessions")) return Response.json([]);
      if (options?.method === "POST")
        person = { ...person, ...JSON.parse(String(options.body)) };
      return Response.json({ person, identities });
    }),
  );
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const updates = await screen.findByRole("checkbox", {
    name: "Email me when there are updates",
  });
  expect(updates).toBeChecked();
  expect(
    screen.getByRole("combobox", { name: "Email for updates" }),
  ).toHaveTextContent("alex@example.test");
  await user.click(updates);
  person = { ...person, display_name: "Alex refreshed" };
  identities = [
    account,
    { ...account, id: "second", email: "second@example.test" },
  ];
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(
    await screen.findByRole("button", { name: "Unlink second@example.test" }),
  ).toBeVisible();
  expect(updates).not.toBeChecked();
  await user.click(screen.getByRole("button", { name: "Save profile" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Profile saved.");
  expect(updates).not.toBeChecked();
});

it("keeps another linked account available after unlinking one from your profile", async () => {
  let identities = [
    account,
    { ...account, id: "second", email: "second@example.test" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: alex,
          auth_mode: "google",
        });
      if (path.endsWith("/sessions")) return Response.json([]);
      if (options?.method === "POST") {
        identities = identities.filter((identity) => identity.id !== "second");
        return new Response(null, { status: 204 });
      }
      return Response.json({ person: alex, identities });
    }),
  );
  window.history.replaceState(null, "", "/profile");
  const user = userEvent.setup();
  render(<App />);
  const unlink = await screen.findByRole("button", {
    name: "Unlink second@example.test",
  });
  expect(unlink).toHaveTextContent(/^Unlink$/);
  await user.click(unlink);
  await user.click(
    within(
      screen.getByRole("dialog", { name: "Unlink second@example.test?" }),
    ).getByRole("button", { name: "Cancel" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(unlink).toHaveFocus();
  await user.click(unlink);
  const dialog = screen.getByRole("dialog", {
    name: "Unlink second@example.test?",
  });
  const confirmUnlink = within(dialog).getByRole("button", {
    name: "Unlink second@example.test",
  });
  expect(confirmUnlink).toHaveTextContent(/^Unlink$/);
  await user.click(confirmUnlink);
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(
    screen.getByRole("button", { name: "Unlink alex@example.test" }),
  ).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "Unlink second@example.test" }),
  ).not.toBeInTheDocument();
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
