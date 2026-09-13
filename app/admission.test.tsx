import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const member = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  update_email: "alex@example.test",
  email_updates: false,
  avatar_url: "",
};
const curator = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};
const account = {
  id: "account",
  provider: "fake",
  email: "alex@example.test",
  created_at: "2026-01-01T00:00:00Z",
};

it("resumes unfinished Onboarding from any signed-in page, hides navigation, and completes once", async () => {
  let person: Record<string, unknown> = member;
  const completions: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/identity/profile"))
        return Response.json({ person, identities: [account] });
      if (path.endsWith("/identity/onboarding")) {
        completions.push(JSON.parse(String(options?.body)));
        person = {
          ...member,
          ...JSON.parse(String(options?.body)),
          onboarding_completed_at: "2026-02-01T00:00:00Z",
        };
        return Response.json(person);
      }
      if (path === "/api/albums") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Welcome to memento" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/welcome");
  expect(document.title).toBe("Welcome | Memento");
  expect(
    screen.queryByRole("navigation", { name: "Main navigation" }),
  ).not.toBeInTheDocument();
  expect(
    await screen.findByText(/Nothing is shared with you yet/),
  ).toBeVisible();
  const name = screen.getByRole("textbox", { name: "Your name" });
  await user.clear(name);
  await user.type(name, "Alex Family");
  await user.click(
    screen.getByRole("checkbox", { name: "Email me when there are updates" }),
  );
  await user.click(screen.getByRole("button", { name: "Continue to memento" }));
  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/albums");
  expect(completions).toEqual([
    {
      display_name: "Alex Family",
      update_email: "alex@example.test",
      email_updates: true,
    },
  ]);
  expect(
    screen.getByRole("navigation", { name: "Main navigation" }),
  ).toBeVisible();
  // A completed Person can no longer reach the Onboarding page.
  window.history.pushState(null, "", "/welcome");
  await user.click(screen.getByRole("link", { name: "memento home" }));
  await waitFor(() => expect(window.location.pathname).toBe("/albums"));
  expect(completions).toHaveLength(1);
});

it("shows pending Access Requests with a badge and approves one by creating a Person", async () => {
  const request = {
    id: "request-1",
    kind: "join",
    provider: "google",
    email: "stranger@example.test",
    email_verified: true,
    display_name: "Stranger",
    person_id: "",
    person_name: "",
    album_id: "",
    album_title: "",
    status: "pending",
    sign_in_count: 3,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-03T00:00:00Z",
    resolved_at: null as string | null,
    resolved_by: "",
  };
  let requests = [request];
  const approvals: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path === "/api/access-requests") return Response.json(requests);
      if (path.endsWith("/approve")) {
        approvals.push(JSON.parse(String(options?.body)));
        requests = [
          {
            ...request,
            status: "approved",
            person_id: "new-person",
            person_name: "Stranger Person",
            resolved_by: "Robin",
            resolved_at: "2026-01-04T00:00:00Z",
          },
        ];
        return Response.json(requests[0]);
      }
      if (path.startsWith("/api/people?")) return Response.json([]);
      if (path === "/api/people/new-person")
        return Response.json({
          person: {
            id: "new-person",
            display_name: "Stranger Person",
            is_curator: false,
            update_email: "",
            email_updates: false,
            avatar_url: "",
          },
          faces: [],
          identities: [],
          preauthorizations: [
            {
              id: "approval",
              email: "stranger@example.test",
              created_at: "2026-01-04T00:00:00Z",
            },
          ],
          invitations: [],
          sessions: [],
          announced: { albums: 0, entries: 0 },
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/requests");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Access requests" }),
  ).toBeVisible();
  const navigation = screen.getByRole("navigation", {
    name: "Main navigation",
  });
  expect(
    await within(navigation).findByRole("link", {
      name: /Requests 1 ?pending/,
    }),
  ).toBeVisible();
  expect(
    screen.getByText("stranger@example.test", { exact: false }),
  ).toBeVisible();
  expect(screen.getByText(/3 sign-ins/)).toBeVisible();
  await user.click(
    screen.getByRole("button", {
      name: "Approve request from stranger@example.test",
    }),
  );
  const dialog = await screen.findByRole("dialog");
  const name = within(dialog).getByRole("textbox", { name: "Display name" });
  expect(name).toHaveValue("Stranger");
  await user.clear(name);
  await user.type(name, "Stranger Person");
  await user.click(
    within(dialog).getByRole("button", { name: "Approve and open person" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Stranger Person" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/curator/people/new-person");
  expect(approvals).toEqual([
    { person_id: "", display_name: "Stranger Person" },
  ]);
  expect(
    screen.getByRole("button", {
      name: "Send invitation to stranger@example.test",
    }),
  ).toBeVisible();
  expect(
    within(navigation).queryByRole("link", { name: /pending/ }),
  ).not.toBeInTheDocument();
});

it("offers an explicit Request access action on an inaccessible Album", async () => {
  const person = { ...member, onboarding_completed_at: "2026-01-01T00:00:00Z" };
  let requested = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: "fake" });
      if (path.endsWith("/request-access") && options?.method === "POST") {
        requested++;
        return Response.json({ id: "request", status: "pending" });
      }
      if (path === "/api/albums/hidden")
        return Response.json(
          { error: { code: "not_found", message: "Album not found." } },
          { status: 404 },
        );
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums/hidden/photos");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Album not available" }),
  ).toBeVisible();
  expect(requested).toBe(0);
  await user.click(screen.getByRole("button", { name: "Request access" }));
  expect(await screen.findByRole("status")).toHaveTextContent(
    "Your Curator has been asked to share this album with you.",
  );
  expect(requested).toBe(1);
  expect(
    screen.queryByRole("button", { name: "Request access" }),
  ).not.toBeInTheDocument();
});
