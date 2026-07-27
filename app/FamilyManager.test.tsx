import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { FamilyManager } from "./FamilyManager";
import type { Relationship } from "./types/generated/family";
import type { Person } from "./types/generated/people";

const contentionWait = { timeout: 5_000 };

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.href;
  return input.url;
}

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") {
    throw new Error("Expected a JSON string request body");
  }
  return body;
}

function person(id: string, name: string): Person {
  return {
    id,
    display_name: name,
    sort_name: name,
    version: 1,
    status: "current",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    roles: [],
    unrevoked_sessions: 0,
    historical_audit_count: 0,
  };
}

function renderManager() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <FamilyManager
        session={{
          display_name: "Curator",
          session_type: "trusted",
          csrf_token: "csrf-token",
        }}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("creates, edits, archives, and inspects relationship-annotated Family branches", async () => {
  const alex = person("11111111-1111-1111-1111-111111111111", "Alex");
  const blair = person("22222222-2222-2222-2222-222222222222", "Blair");
  let relationships: Relationship[] = [];
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.spyOn(window, "confirm").mockReturnValue(true);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/people?query=&include_archived=false") {
        return jsonResponse({ people: [alex, blair] });
      }
      if (path.startsWith("/api/family/relationships?")) {
        return jsonResponse({ relationships });
      }
      if (path === "/api/family/relationships" && init?.method === "POST") {
        const request = JSON.parse(stringBody(init.body)) as {
          relationship_type: string;
          person_a_id: string;
          person_b_id: string;
          partner_status: string;
        };
        const created: Relationship = {
          id: "33333333-3333-3333-3333-333333333333",
          relationship_type: request.relationship_type,
          person_a: alex,
          person_b: blair,
          version: 1,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        };
        relationships = [created];
        return jsonResponse(created, 201);
      }
      if (
        path ===
          "/api/family/relationships/33333333-3333-3333-3333-333333333333" &&
        init?.method === "PATCH"
      ) {
        const updated: Relationship = {
          ...relationships[0],
          relationship_type: "partner",
          partner_status: "former",
          version: 2,
        };
        relationships = [updated];
        return jsonResponse(updated);
      }
      if (path.endsWith("/archive") && init?.method === "POST") {
        const archived = {
          ...relationships[0],
          version: 3,
          archived_at: "2026-01-02T00:00:00Z",
        };
        relationships = [];
        return jsonResponse(archived);
      }
      if (path === `/api/family/branches/${alex.id}`) {
        return jsonResponse({
          root: alex,
          members: [
            {
              person: blair,
              connection_type: "descendant_current_partner",
              generation: 2,
            },
          ],
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderManager();
  expect(screen.getAllByText(/never grants Recipient access/i)).toHaveLength(2);
  expect(
    screen.getByText(
      /Former partners and explicitly recorded siblings do not/i,
    ),
  ).toBeInTheDocument();

  await screen.findAllByRole("option", { name: "Alex (Alex)" }, contentionWait);
  fireEvent.change(screen.getByLabelText("Connection type"), {
    target: { value: "sibling" },
  });
  fireEvent.change(screen.getByLabelText("First Person"), {
    target: { value: alex.id },
  });
  fireEvent.change(screen.getByLabelText("Second Person"), {
    target: { value: blair.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create connection" }));

  const sibling = await screen.findByRole(
    "button",
    { name: /Alex and Blair are siblings/ },
    contentionWait,
  );
  const createRequest = requests.find(
    ({ path, init }) =>
      path === "/api/family/relationships" && init?.method === "POST",
  );
  expect(JSON.parse(stringBody(createRequest?.init?.body))).toEqual({
    relationship_type: "sibling",
    person_a_id: alex.id,
    person_b_id: blair.id,
    partner_status: "",
  });
  expect(createRequest?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );

  fireEvent.click(sibling);
  fireEvent.change(screen.getByLabelText("Connection type"), {
    target: { value: "partner" },
  });
  fireEvent.change(screen.getByLabelText("Partner connection"), {
    target: { value: "former" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save connection" }));
  const former = await screen.findByRole(
    "button",
    { name: /Alex and Blair are former partners/ },
    contentionWait,
  );
  const updateRequest = requests.find(({ init }) => init?.method === "PATCH");
  expect(JSON.parse(stringBody(updateRequest?.init?.body))).toEqual({
    relationship_type: "partner",
    person_a_id: alex.id,
    person_b_id: blair.id,
    partner_status: "former",
    version: 1,
  });

  fireEvent.click(former);
  fireEvent.click(screen.getByRole("button", { name: "Archive connection" }));
  await waitFor(
    () =>
      expect(requests.some(({ path }) => path.endsWith("/archive"))).toBe(true),
    contentionWait,
  );

  fireEvent.change(screen.getByLabelText("Branch root"), {
    target: { value: alex.id },
  });
  expect(
    await screen.findByText(
      "Current partner of a generation 2 descendant",
      {},
      contentionWait,
    ),
  ).toBeInTheDocument();
  expect(screen.getByText("Alex's Family branch")).toBeInTheDocument();
}, 15_000);

test("shows a rejected cycle without implying that the connection was saved", async () => {
  const alex = person("11111111-1111-1111-1111-111111111111", "Alex");
  const blair = person("22222222-2222-2222-2222-222222222222", "Blair");
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      if (path === "/api/people?query=&include_archived=false") {
        return jsonResponse({ people: [alex, blair] });
      }
      if (path.startsWith("/api/family/relationships?")) {
        return jsonResponse({ relationships: [] });
      }
      if (path === "/api/family/relationships" && init?.method === "POST") {
        return jsonResponse(
          {
            error: {
              message:
                "That parent-child connection would create a cycle. The Family graph was not changed.",
            },
          },
          409,
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderManager();
  await screen.findAllByRole("option", { name: "Alex (Alex)" }, contentionWait);
  fireEvent.change(screen.getByLabelText("Parent"), {
    target: { value: alex.id },
  });
  fireEvent.change(screen.getByLabelText("Child"), {
    target: { value: blair.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create connection" }));

  expect(
    await screen.findByRole("alert", {}, contentionWait),
  ).toHaveTextContent(/would create a cycle.*was not changed/i);
  expect(
    screen.getByText("No Family relationships recorded."),
  ).toBeInTheDocument();
});
