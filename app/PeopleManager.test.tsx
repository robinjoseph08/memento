import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
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
  const familyKeys = [
    ["family-people", ""],
    ["family-relationships", false],
    ["family-branch", source.id],
  ] as const;
  for (const key of familyKeys) client.setQueryData(key, { cached: true });
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
  for (const key of familyKeys) {
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  }
}, 15_000);
