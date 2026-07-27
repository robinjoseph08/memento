import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { PeopleManager } from "./PeopleManager";
import type { MergePreview, Person } from "./types/generated/people";

const contentionWait = { timeout: 5_000 };

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") {
    return input;
  }
  if (input instanceof URL) {
    return input.href;
  }
  return input.url;
}

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") {
    throw new Error("Expected a JSON string request body");
  }
  return body;
}

function renderManager() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <PeopleManager
          session={{
            display_name: "Curator",
            session_type: "trusted",
            csrf_token: "csrf-token",
            curator: true,
            onboarding_required: false,
          }}
        />
      </QueryClientProvider>,
    ),
  };
}

function person(id: string, displayName: string, sortName: string): Person {
  return {
    id,
    display_name: displayName,
    sort_name: sortName,
    version: 1,
    status: "current",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    roles: [],
    unrevoked_sessions: 0,
    historical_audit_count: 0,
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("discloses archive effects and invalidates Visibility caches", async () => {
  const alex = person("33333333-3333-3333-3333-333333333333", "Alex", "Alex");
  let archived = false;
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      if (path.startsWith("/api/people?") && !init?.method) {
        return Promise.resolve(
          jsonResponse({ people: archived ? [] : [alex] }),
        );
      }
      if (path === `/api/people/${alex.id}/archive`) {
        archived = true;
        return Promise.resolve(
          jsonResponse({
            ...alex,
            status: "archived",
            version: 2,
            archived_at: "2026-01-02T00:00:00Z",
          }),
        );
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  const { client } = renderManager();
  const visibilityKeys = [
    ["visibility-people"],
    ["visibility-circles"],
    ["curator-interest-list", alex.id],
    ["curator-discoverable", alex.id],
  ] as const;
  for (const key of visibilityKeys) client.setQueryData(key, { cached: true });
  const directory = await screen.findByLabelText("People directory");
  fireEvent.click(
    await within(directory).findByRole("button", { name: /Alex/ }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Archive Person" }));

  await waitFor(() => expect(archived).toBe(true));
  expect(confirm).toHaveBeenCalledWith(
    expect.stringMatching(
      /removes the Person from Visibility circles, and may deactivate Interest choices/,
    ),
  );
  for (const key of visibilityKeys) {
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  }
});

test("designates a Pending Recipient separately from sending an Invitation", async () => {
  const alex = person("33333333-3333-3333-3333-333333333333", "Alex", "Alex");
  const access = { id: "access-id", generation: 1, state: "pending" };
  let designated = false;
  let invitationSent = false;
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/people?") && !init?.method) {
        return Promise.resolve(
          jsonResponse({
            people: [
              designated
                ? {
                    ...alex,
                    roles: ["recipient"],
                    current_recipient_access: access,
                    current_login_email: "alex@example.com",
                  }
                : alex,
            ],
          }),
        );
      }
      if (path.endsWith("/designate")) {
        designated = true;
        return Promise.resolve(
          jsonResponse(
            {
              person_id: alex.id,
              person_name: "Alex",
              email: "alex@example.com",
              access,
            },
            201,
          ),
        );
      }
      if (path === `/api/recipients/${alex.id}`) {
        return Promise.resolve(
          jsonResponse({
            person_id: alex.id,
            person_name: "Alex",
            email: "alex@example.com",
            access,
            ...(invitationSent
              ? {
                  invitation: {
                    id: "invitation-id",
                    status: "active",
                    issued_at: "2026-07-27T12:00:00Z",
                    expires_at: "2026-08-10T12:00:00Z",
                    automatic_reminder_scheduled_at: "2026-08-03T12:00:00Z",
                    manual_reminder_count: 0,
                    initial_delivery: {
                      status: "failed",
                      attempts: 1,
                      failure: "recipient_rejected",
                    },
                    automatic_reminder_delivery: {
                      status: "queued",
                      attempts: 0,
                    },
                  },
                }
              : {}),
          }),
        );
      }
      if (path.endsWith("/invitation/send")) {
        invitationSent = true;
        return Promise.resolve(jsonResponse({ status: "active" }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderManager();
  fireEvent.click(
    await screen.findByRole("button", { name: /^Alex/ }, contentionWait),
  );
  fireEvent.change(screen.getByLabelText("Login email"), {
    target: { value: "alex@example.com" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Designate Pending Recipient" }),
  );

  const send = await screen.findByRole(
    "button",
    { name: "Create and send Invitation" },
    contentionWait,
  );
  const designateRequest = requests.find(({ path }) =>
    path.endsWith("/designate"),
  );
  expect(JSON.parse(stringBody(designateRequest?.init?.body))).toEqual({
    email: "alex@example.com",
  });
  expect(designateRequest?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );
  expect(requests.some(({ path }) => path.endsWith("/invitation/send"))).toBe(
    false,
  );

  fireEvent.click(send);
  const revoke = await screen.findByRole(
    "button",
    { name: "Revoke Invitation" },
    contentionWait,
  );
  expect(revoke.closest(".invitation-status")).toHaveTextContent(
    "Invitation: active",
  );
  expect(revoke.closest(".invitation-status")).toHaveTextContent(
    "Initial delivery: Failed (recipient rejected)",
  );
  expect(revoke.closest(".invitation-status")).toHaveTextContent(
    "Automatic reminder: Scheduled",
  );
  const sendRequest = requests.find(({ path }) =>
    path.endsWith("/invitation/send"),
  );
  expect(sendRequest?.init).toMatchObject({
    method: "POST",
    headers: { "X-Memento-CSRF": "csrf-token" },
  });
});

test("wires every Invitation control to its exact mutation", async () => {
  const alex = {
    ...person("33333333-3333-3333-3333-333333333333", "Alex", "Alex"),
    roles: ["recipient"],
    current_recipient_access: {
      id: "access-id",
      generation: 1,
      state: "pending",
    },
    current_login_email: "alex@example.com",
  };
  let status = "active";
  let invitationID = "invitation-id";
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/people?") && !init?.method) {
        return Promise.resolve(jsonResponse({ people: [alex] }));
      }
      if (path === `/api/recipients/${alex.id}` && !init?.method) {
        return Promise.resolve(
          jsonResponse({
            person_id: alex.id,
            person_name: "Alex",
            email: "alex@example.com",
            access: alex.current_recipient_access,
            invitation: {
              id: invitationID,
              status,
              issued_at: "2026-07-27T12:00:00Z",
              expires_at: "2026-08-10T12:00:00Z",
              sent_at: "2026-07-27T12:01:00Z",
              automatic_reminder_scheduled_at: "2026-08-03T12:00:00Z",
              manual_reminder_count: 0,
            },
          }),
        );
      }
      if (path.includes("/invitation/") && init?.method === "POST") {
        if (path.endsWith("/revoke")) status = "revoked";
        if (path.endsWith("/reissue")) {
          status = "active";
          invitationID = "replacement-id";
        }
        return Promise.resolve(jsonResponse({ status }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  renderManager();
  fireEvent.click(
    await screen.findByRole("button", { name: /^Alex/ }, contentionWait),
  );
  for (const control of [
    ["Send manual reminder", "/remind"],
    ["Reissue with new link", "/reissue"],
    ["Revoke Invitation", "/revoke"],
  ] as const) {
    fireEvent.click(
      await screen.findByRole("button", { name: control[0] }, contentionWait),
    );
    await waitFor(
      () =>
        expect(requests.some(({ path }) => path.endsWith(control[1]))).toBe(
          true,
        ),
      contentionWait,
    );
  }
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: "Reissue Invitation" },
      contentionWait,
    ),
  );
  await waitFor(
    () =>
      expect(
        requests.filter(({ path }) => path.endsWith("/reissue")),
      ).toHaveLength(2),
    contentionWait,
  );
  const actionRequests = requests.filter(({ path }) =>
    path.includes("/invitation/"),
  );
  for (const request of actionRequests) {
    expect(request.init).toMatchObject({
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Memento-CSRF": "csrf-token",
      },
    });
  }
  expect(
    actionRequests.find(({ path }) => path.endsWith("/remind"))?.init?.body,
  ).toBe(JSON.stringify({ invitation_id: "invitation-id" }));
  expect(
    actionRequests.find(({ path }) => path.endsWith("/revoke"))?.init?.body,
  ).toBe(JSON.stringify({ invitation_id: "replacement-id" }));
  const reissues = actionRequests.filter(({ path }) =>
    path.endsWith("/reissue"),
  );
  expect(reissues[0]?.init?.body).toBe(
    JSON.stringify({ invitation_id: "invitation-id" }),
  );
  expect(reissues[1]?.init?.body).toBe(
    JSON.stringify({ invitation_id: "replacement-id" }),
  );
});

test("previews and confirms the exact source, survivor, generation, email, and versions", async () => {
  const source = {
    ...person(
      "11111111-1111-1111-1111-111111111111",
      "Robin",
      "Robin, Duplicate",
    ),
    roles: ["recipient"],
    current_recipient_access: {
      id: "access-id",
      generation: 2,
      state: "completed",
    },
    current_login_email: "duplicate@example.com",
  };
  const survivor = person(
    "22222222-2222-2222-2222-222222222222",
    "Robin",
    "Robin, Survivor",
  );
  const preview: MergePreview = {
    source,
    survivor,
    affected_references: {
      current_recipient_generation_id: "access-id",
      sessions_invalidated: 2,
      historical_audit_rows_preserved: 3,
      source_roles: ["recipient"],
      survivor_roles: [],
      recipient_role_will_transfer: true,
      resulting_recipient_generation: 4,
      family_relationships_moved: 2,
      family_relationships_archived: 1,
      family_reference_fingerprint: "a".repeat(64),
      visibility_memberships_moved: 1,
      interest_entries_moved: 1,
      interest_history_owners_retained: 0,
      visibility_reference_fingerprint: "b".repeat(64),
    },
    requires_generation_transfer: true,
    requires_email_resolution: true,
    source_email: "duplicate@example.com",
    survivor_email: "survivor@example.com",
    sessions_will_be_invalidated: true,
    roles_will_not_be_unioned: true,
    audience_authority_unchanged: true,
    current_curator_session_kept: false,
    preview_fingerprint: "f".repeat(64),
    can_merge: true,
    blockers: [],
  };
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/people?") && !init?.method) {
        return Promise.resolve(jsonResponse({ people: [source, survivor] }));
      }
      if (path === "/api/people/merge-preview") {
        return Promise.resolve(jsonResponse(preview));
      }
      if (path === "/api/people/merge") {
        return Promise.resolve(jsonResponse({ ...survivor, version: 2 }));
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  const { client } = renderManager();
  const relatedQueryKeys = [
    ["family-people", ""],
    ["family-relationships", false],
    ["family-branch", source.id],
    ["visibility-people"],
    ["visibility-circles"],
    ["curator-interest-list", source.id],
    ["curator-discoverable", source.id],
  ] as const;
  for (const key of relatedQueryKeys)
    client.setQueryData(key, { cached: true });
  await screen.findAllByRole("option", { name: /11111111/ }, contentionWait);
  const sourceSelect = screen.getByLabelText("Source Person");
  const survivorSelect = screen.getByLabelText("Survivor Person");
  fireEvent.change(sourceSelect, { target: { value: source.id } });
  await waitFor(
    () => expect(sourceSelect).toHaveValue(source.id),
    contentionWait,
  );
  fireEvent.change(survivorSelect, { target: { value: survivor.id } });
  await waitFor(
    () => expect(survivorSelect).toHaveValue(survivor.id),
    contentionWait,
  );
  const previewButton = screen.getByRole("button", { name: "Preview merge" });
  await waitFor(() => expect(previewButton).toBeEnabled(), contentionWait);
  fireEvent.click(previewButton);

  expect(
    await screen.findByText(/will become generation 4/, {}, contentionWait),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/2 Family relationship references/),
  ).toBeInTheDocument();
  expect(screen.getByText(/1 active Family relationships/)).toBeInTheDocument();
  const confirm = screen.getByRole("button", { name: "Confirm audited merge" });
  expect(confirm).toBeDisabled();
  fireEvent.click(
    screen.getByLabelText(
      "Explicitly transfer current Recipient access generation",
    ),
  );
  fireEvent.change(screen.getByLabelText("Login email after transfer"), {
    target: { value: "keep_survivor" },
  });
  expect(confirm).toBeEnabled();
  fireEvent.click(confirm);

  await waitFor(
    () =>
      expect(requests.some(({ path }) => path === "/api/people/merge")).toBe(
        true,
      ),
    contentionWait,
  );
  const previewRequest = requests.find(
    ({ path }) => path === "/api/people/merge-preview",
  );
  expect(JSON.parse(stringBody(previewRequest?.init?.body))).toEqual({
    source_person_id: source.id,
    survivor_person_id: survivor.id,
  });
  expect(previewRequest?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );
  const mergeRequest = requests.find(
    ({ path }) => path === "/api/people/merge",
  );
  expect(JSON.parse(stringBody(mergeRequest?.init?.body))).toEqual({
    source_person_id: source.id,
    survivor_person_id: survivor.id,
    source_version: 1,
    survivor_version: 1,
    transfer_current_access_generation: true,
    expected_recipient_generation: 4,
    preview_fingerprint: "f".repeat(64),
    email_resolution: "keep_survivor",
  });
  expect(mergeRequest?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );
  for (const key of relatedQueryKeys) {
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  }
}, 15_000);
