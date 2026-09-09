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
import type { Preauthorization } from "./types/generated/identity";

const robin = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  update_email: "robin@example.test",
  email_updates: true,
};
const alex = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  update_email: "updates@example.test",
  email_updates: false,
};
const account = {
  id: "account",
  provider: "fake",
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

it("chooses an avatar from a Person's linked Immich faces", async () => {
  let detail = {
    person: { ...alex, avatar_url: "/api/media/people/alex/avatar?v=first" },
    identities: [account],
    sessions: [],
    preauthorizations: [],
    faces: [
      {
        source_face_id: "first",
        source_name: "First face",
        thumbnail_url: "/api/media/faces/first/thumbnail",
        avatar: true,
      },
      {
        source_face_id: "second",
        source_name: "Second face",
        thumbnail_url: "/api/media/faces/second/thumbnail",
        avatar: false,
      },
    ],
  };
  let avatarBody: unknown;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: robin,
          auth_mode: "fake",
        });
      if (path === "/api/people/alex/avatar") {
        avatarBody = JSON.parse(String(options?.body));
        detail = {
          ...detail,
          person: {
            ...detail.person,
            avatar_url: "/api/media/people/alex/avatar?v=second",
          },
          faces: detail.faces.map((face) => ({
            ...face,
            avatar: face.source_face_id === "second",
          })),
        };
      }
      return Response.json(detail);
    }),
  );
  window.history.replaceState(null, "", "/curator/people/alex");
  const user = userEvent.setup();
  render(<App />);

  await user.click(
    await screen.findByRole("radio", { name: "Use Second face as avatar" }),
  );
  await user.click(screen.getByRole("button", { name: "Save avatar" }));
  await waitFor(() => expect(avatarBody).toEqual({ source_face_id: "second" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Avatar saved.");
});

it("puts linked accounts first and keeps previous emails collapsed outside active approvals", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: robin,
          auth_mode: "fake",
        });
      return Response.json({
        person: alex,
        identities: [account],
        sessions: [],
        preauthorizations: [
          {
            id: "active",
            email: "New.Email@example.test",
            created_at: "2026-01-01T00:00:00Z",
          },
          {
            id: "used",
            email: "used@example.test",
            created_at: "2026-01-01T00:00:00Z",
            consumed_at: "2026-01-02T00:00:00Z",
          },
          {
            id: "revoked",
            email: "revoked@example.test",
            created_at: "2026-01-01T00:00:00Z",
            revoked_at: "2026-01-02T00:00:00Z",
          },
        ],
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/people/alex");
  const user = userEvent.setup();
  render(<App />);
  const active = await screen.findByRole("table", {
    name: "Preauthorizations",
  });
  expect(within(active).getAllByRole("row")).toHaveLength(2);
  expect(within(active).getByText("New.Email@example.test")).toBeVisible();
  expect(
    within(active).getByRole("button", {
      name: "Revoke New.Email@example.test",
    }),
  ).toHaveTextContent(/^Revoke$/);
  expect(
    screen.getByRole("button", { name: "Unlink alex@example.test" }),
  ).toBeEnabled();
  expect(screen.queryByText("Development")).not.toBeInTheDocument();
  const headings = screen
    .getAllByRole("heading", { level: 2 })
    .map((heading) => heading.textContent);
  expect(headings.indexOf("Linked accounts")).toBeLessThan(
    headings.indexOf("Preauthorizations"),
  );
  const summary = screen.getByText("Previous emails (2)");
  expect(screen.getByText("used@example.test")).not.toBeVisible();
  expect(screen.getByText("revoked@example.test")).not.toBeVisible();
  await user.click(summary);
  const history = screen.getByRole("table", { name: "Previous emails" });
  expect(within(history).getByRole("cell", { name: "Consumed" })).toBeVisible();
  expect(within(history).getByRole("cell", { name: "Revoked" })).toBeVisible();
  expect(within(history).queryByRole("button")).not.toBeInTheDocument();
});

it.each([false, true])(
  "shows read-only notification preferences and sessions in Person details, subscribed=%s",
  async (subscribed) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path.endsWith("/status"))
          return Response.json({
            claimed: true,
            person: robin,
            auth_mode: "fake",
          });
        if (path !== "/api/people/alex")
          throw new Error(`Unexpected request: ${path}`);
        return Response.json({
          person: { ...alex, email_updates: subscribed },
          identities: [account],
          preauthorizations: [],
          sessions: [
            {
              id: "browser",
              identity_id: account.id,
              email: account.email,
              device: "Alex's phone",
              current: false,
              created_at: "2026-01-01T00:00:00Z",
              last_used_at: "2026-01-02T00:00:00Z",
              expires_at: "2026-02-01T00:00:00Z",
            },
          ],
        });
      }),
    );
    window.history.replaceState(null, "", "/curator/people/alex");
    render(<App />);
    const notifications = await screen.findByRole("region", {
      name: "Notifications",
    });
    expect(document.title).toBe("Alex | Memento");
    expect(
      within(notifications).getByText("updates@example.test"),
    ).toBeVisible();
    expect(
      within(notifications).getByText(
        subscribed ? "Subscribed" : "Not subscribed",
      ),
    ).toBeVisible();
    expect(
      within(notifications).queryByRole("checkbox"),
    ).not.toBeInTheDocument();
    expect(
      within(notifications).queryByRole("combobox"),
    ).not.toBeInTheDocument();
    const sessions = screen.getByRole("region", { name: "Browser sessions" });
    const table = within(sessions).getByRole("table", {
      name: "Browser sessions",
    });
    expect(within(table).getByText("Alex's phone")).toBeVisible();
    expect(
      within(table).getByRole("columnheader", { name: "Expires" }),
    ).toBeVisible();
    expect(within(sessions).queryByRole("button")).not.toBeInTheDocument();
    expect(
      within(sessions).queryByText("This browser"),
    ).not.toBeInTheDocument();
  },
);

it("lets a Curator remove another person's final linked account without losing a name draft", async () => {
  let identities = [account];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: robin,
          auth_mode: "fake",
        });
      if (options?.method === "POST") {
        identities = [];
        return new Response(null, { status: 204 });
      }
      return Response.json({
        person: alex,
        identities,
        preauthorizations: [],
        sessions: [],
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/people/alex");
  const user = userEvent.setup();
  render(<App />);
  const name = await screen.findByRole("textbox", { name: "Display name" });
  await user.type(name, " edited");
  expect(screen.getByRole("checkbox", { name: "Curator" })).toBeEnabled();
  expect(
    screen.getByRole("checkbox", { name: "Deactivate this person" }),
  ).toBeEnabled();
  await user.click(
    screen.getByRole("button", { name: "Unlink alex@example.test" }),
  );
  const dialog = screen.getByRole("dialog", {
    name: "Unlink alex@example.test?",
  });
  await user.click(
    within(dialog).getByRole("button", { name: "Unlink alex@example.test" }),
  );
  expect(await screen.findByText("No accounts linked yet.")).toBeVisible();
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(name).toHaveValue("Alex edited");
});

it("keeps an exact-email draft through refresh and field errors, then moves a revoked approval to history", async () => {
  let failSave = true;
  let failRead = false;
  let preauthorizations: Preauthorization[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: robin,
          auth_mode: "fake",
        });
      if (options?.method === "POST") {
        if (path.endsWith("/revoke")) {
          preauthorizations = preauthorizations.map((authorization) => ({
            ...authorization,
            revoked_at: "2026-01-02T00:00:00Z",
          }));
          return new Response(null, { status: 204 });
        }
        if (failSave)
          return Response.json(
            {
              error: {
                fields: {
                  email: "This email is already approved for another person.",
                },
              },
            },
            { status: 400 },
          );
        const authorization = {
          id: "approved",
          email: JSON.parse(String(options.body)).email as string,
          created_at: "2026-01-01T00:00:00Z",
        };
        preauthorizations = [authorization];
        return Response.json(authorization);
      }
      if (failRead) return Response.json({}, { status: 503 });
      return Response.json({
        person: alex,
        identities: [],
        preauthorizations,
        sessions: [],
      });
    }),
  );
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  window.history.replaceState(null, "", "/curator/people/alex");
  const user = userEvent.setup();
  render(<App />);
  const email = await screen.findByRole("textbox", {
    name: "Google email address",
  });
  await user.type(email, "Exact.Email@example.test");
  await user.click(screen.getByRole("link", { name: "People" }));
  expect(confirm).toHaveBeenCalledOnce();
  expect(email).toHaveValue("Exact.Email@example.test");
  failRead = true;
  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong.",
  );
  expect(email).toHaveValue("Exact.Email@example.test");
  failRead = false;
  await user.click(screen.getByRole("button", { name: "Try again" }));
  await user.click(screen.getByRole("button", { name: "Preauthorize email" }));
  expect(
    await screen.findByText(
      "This email is already approved for another person.",
    ),
  ).toBeVisible();
  expect(email).toHaveFocus();
  expect(email).toHaveValue("Exact.Email@example.test");
  failSave = false;
  await user.click(screen.getByRole("button", { name: "Preauthorize email" }));
  const active = await screen.findByRole("table", {
    name: "Preauthorizations",
  });
  expect(within(active).getByText("Exact.Email@example.test")).toBeVisible();
  await waitFor(() => expect(email).toHaveValue(""));
  await user.click(
    within(active).getByRole("button", {
      name: "Revoke Exact.Email@example.test",
    }),
  );
  const dialog = screen.getByRole("dialog", {
    name: "Revoke Exact.Email@example.test?",
  });
  await user.click(
    within(dialog).getByRole("button", {
      name: "Revoke Exact.Email@example.test",
    }),
  );
  expect(await screen.findByText("No unused email approvals.")).toBeVisible();
  expect(
    screen.queryByRole("table", { name: "Preauthorizations" }),
  ).not.toBeInTheDocument();
  await user.click(screen.getByText("Previous emails (1)"));
  expect(screen.getByRole("cell", { name: "Revoked" })).toBeVisible();
  expect(screen.getByText("Exact.Email@example.test")).toBeVisible();
});

it("lets a Curator rename themselves without removing their own access", async () => {
  let person = robin;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (options?.method === "POST") {
        person = { ...person, ...JSON.parse(String(options.body)) };
        return Response.json(person);
      }
      return Response.json({
        person,
        identities: [{ ...account, email: robin.update_email }],
        preauthorizations: [],
        sessions: [],
      });
    }),
  );
  window.history.replaceState(null, "", "/curator/people/robin");
  const user = userEvent.setup();
  render(<App />);
  const role = await screen.findByRole("checkbox", { name: "Curator" });
  expect(role).toBeDisabled();
  expect(role).toBeChecked();
  const deactivate = screen.getByRole("checkbox", {
    name: "Deactivate this person",
  });
  expect(deactivate).toBeDisabled();
  expect(deactivate).not.toBeChecked();
  expect(
    screen.getByText(
      "You can't remove your own Curator role or deactivate yourself.",
    ),
  ).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Unlink robin@example.test" }),
  ).toBeDisabled();
  const name = screen.getByRole("textbox", { name: "Display name" });
  await user.clear(name);
  await user.type(name, "Robin edited{Enter}");
  expect(await screen.findByRole("status")).toHaveTextContent("Person saved.");
  expect(document.title).toBe("Robin edited | Memento");
  expect(name).toHaveValue("Robin edited");
  expect(role).toBeChecked();
  expect(deactivate).not.toBeChecked();
});
