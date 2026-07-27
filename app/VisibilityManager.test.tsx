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

import {
  RecipientVisibilityManager,
  VisibilityManager,
} from "./VisibilityManager";
import type { Person as CuratorPerson } from "./types/generated/people";
import type {
  Circle,
  InterestListResponse,
} from "./types/generated/visibility";

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

function curatorPerson(
  id: string,
  name: string,
  roles: string[] = [],
): CuratorPerson {
  return {
    id,
    display_name: name,
    sort_name: name,
    version: 1,
    status: "current",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    roles,
    unrevoked_sessions: 0,
    historical_audit_count: 0,
  };
}

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected JSON body");
  return JSON.parse(body) as Record<string, unknown>;
}

function renderManager() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <VisibilityManager
        session={{
          display_name: "Curator",
          session_type: "trusted",
          csrf_token: "csrf-token",
          curator: true,
        }}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("manages overlapping circle membership with a desktop matrix and mobile filters", async () => {
  const curator = curatorPerson(
    "11111111-1111-4111-8111-111111111111",
    "Curator",
    ["curator", "recipient"],
  );
  const recipient = curatorPerson(
    "22222222-2222-4222-8222-222222222222",
    "Recipient",
    ["recipient"],
  );
  const alex = curatorPerson("33333333-3333-4333-8333-333333333333", "Alex");
  let circles: Circle[] = [
    {
      id: "44444444-4444-4444-8444-444444444444",
      name: "Family",
      version: 1,
      members: [recipient],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    },
    {
      id: "55555555-5555-4555-8555-555555555555",
      name: "Cousins",
      version: 1,
      members: [alex],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    },
  ];
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/people?query=&include_archived=false") {
        return jsonResponse({ people: [alex, curator, recipient] });
      }
      if (path.startsWith("/api/visibility-circles?")) {
        return jsonResponse({ circles });
      }
      if (path === "/api/visibility-circles" && init?.method === "POST") {
        const created: Circle = {
          id: "66666666-6666-4666-8666-666666666666",
          name: String(stringBody(init.body).name),
          version: 1,
          members: [],
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        };
        circles = [...circles, created];
        return jsonResponse(created, 201);
      }
      if (path.includes("/members/") && init?.method === "PUT") {
        const included = Boolean(stringBody(init.body).included);
        circles = circles.map((circle) =>
          path.includes(circle.id)
            ? {
                ...circle,
                version: circle.version + 1,
                members: included
                  ? [...circle.members, alex]
                  : circle.members.filter((person) => person.id !== alex.id),
              }
            : circle,
        );
        return jsonResponse(circles.find((circle) => path.includes(circle.id)));
      }
      if (path.includes(circles[0].id) && init?.method === "PATCH") {
        const body = stringBody(init.body);
        circles = circles.map((circle, index) =>
          index === 0
            ? {
                ...circle,
                name: String(body.name),
                version: circle.version + 1,
              }
            : circle,
        );
        return jsonResponse(circles[0]);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  const { container } = renderManager();
  const familyMembership = await screen.findByLabelText("Alex in Family");
  expect(familyMembership).not.toBeChecked();
  expect(screen.getByLabelText("Alex in Cousins")).toBeChecked();
  fireEvent.click(familyMembership);
  await waitFor(() =>
    expect(screen.getByLabelText("Alex in Family")).toBeChecked(),
  );
  const membershipRequest = requests.find(({ path }) =>
    path.includes("/members/"),
  );
  expect(stringBody(membershipRequest?.init?.body)).toEqual({
    included: true,
    version: 1,
  });
  expect(membershipRequest?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );

  fireEvent.change(screen.getByLabelText("Filter People"), {
    target: { value: "recip" },
  });
  const filteredMembers = container.querySelector<HTMLElement>(
    ".visibility-filtered-members",
  );
  expect(filteredMembers).not.toBeNull();
  expect(within(filteredMembers!).getByText("Recipient")).toBeInTheDocument();
  expect(within(filteredMembers!).queryByText("Alex")).not.toBeInTheDocument();
  expect(
    within(filteredMembers!).queryByText("Curator"),
  ).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Edit Family" }));
  fireEvent.change(screen.getByLabelText("Circle name"), {
    target: { value: "Updated Family" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save circle" }));
  expect(
    await screen.findByRole("button", { name: "Edit Updated Family" }),
  ).toBeInTheDocument();
  const updateRequest = requests.find(({ init }) => init?.method === "PATCH");
  expect(stringBody(updateRequest?.init?.body)).toEqual({
    name: "Updated Family",
    version: 2,
  });

  fireEvent.change(screen.getByLabelText("New circle name"), {
    target: { value: "Grandparents" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create circle" }));
  expect(
    await screen.findByRole("button", { name: "Edit Grandparents" }),
  ).toBeInTheDocument();
  const createRequest = requests.find(
    ({ path, init }) =>
      path === "/api/visibility-circles" && init?.method === "POST",
  );
  expect(stringBody(createRequest?.init?.body)).toEqual({
    name: "Grandparents",
  });
});

test("lets a Recipient edit only their own discoverable Interest choices", async () => {
  const recipient = curatorPerson(
    "22222222-2222-4222-8222-222222222222",
    "Recipient",
    ["recipient"],
  );
  const alex = {
    id: "33333333-3333-4333-8333-333333333333",
    display_name: "Alex",
    sort_name: "Alex",
    relationship: { connection_type: "sibling", generation: 0 },
  };
  const blair = {
    id: "44444444-4444-4444-8444-444444444444",
    display_name: "Blair",
    sort_name: "Blair",
  };
  const nextCursor = "opaque/cursor+value";
  let interest: InterestListResponse = {
    recipient,
    version: 0,
    entries: [],
    history: [],
    history_next_cursor: "older-history",
  };
  let resolveOlderHistory: (response: Response) => void = () => undefined;
  const olderHistory = new Promise<Response>((resolve) => {
    resolveOlderHistory = resolve;
  });
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.startsWith("/api/me/people?")) {
        const cursor = new URL(path, "https://memento.test").searchParams.get(
          "cursor",
        );
        return cursor === nextCursor
          ? jsonResponse({ people: [blair] })
          : jsonResponse({ people: [alex], next_cursor: nextCursor });
      }
      if (path === "/api/me/interest-list") {
        return jsonResponse(interest);
      }
      if (path.startsWith("/api/me/interest-list?")) {
        return olderHistory;
      }
      if (path === "/api/session/logout" && init?.method === "POST") {
        return new Response(null, { status: 204 });
      }
      if (
        path === `/api/me/interest-list/${alex.id}` &&
        init?.method === "PUT"
      ) {
        interest = {
          recipient,
          version: 1,
          entries: [
            {
              person: alex,
              state: "active",
              chosen_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            },
          ],
          history: [
            {
              id: 1,
              person: alex,
              actor: recipient,
              action: "selected",
              result: "active",
              reason: "explicit",
              created_at: "2026-01-01T00:00:00Z",
            },
          ],
        };
        return jsonResponse(interest);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const onSignOut = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <RecipientVisibilityManager
        onSignOut={onSignOut}
        session={{
          display_name: "Recipient",
          session_type: "trusted",
          csrf_token: "recipient-csrf",
          curator: false,
        }}
      />
    </QueryClientProvider>,
  );

  const alexChoice = await screen.findByRole("checkbox", { name: /Alex/ });
  expect(await screen.findByRole("checkbox", { name: /Blair/ })).toBeVisible();
  expect(screen.getByText("sibling")).toBeInTheDocument();
  expect(
    requests.some((request) =>
      request.path.includes("cursor=opaque%2Fcursor%2Bvalue"),
    ),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Load older history" }));
  fireEvent.click(alexChoice);
  await waitFor(() => expect(alexChoice).toBeChecked());
  resolveOlderHistory(
    jsonResponse({
      recipient,
      version: 0,
      entries: [],
      history: [],
    }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Load older history" }),
    ).not.toBeInTheDocument(),
  );
  expect(alexChoice).toBeChecked();
  const mutation = requests.find(({ init }) => init?.method === "PUT");
  expect(mutation?.path).toBe(`/api/me/interest-list/${alex.id}`);
  expect(stringBody(mutation?.init?.body)).toEqual({
    selected: true,
    version: 0,
  });
  expect(mutation?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "recipient-csrf" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  await waitFor(() => expect(onSignOut).toHaveBeenCalledOnce());
  expect(requests).toContainEqual(
    expect.objectContaining({
      path: "/api/session/logout",
      init: expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "X-Memento-CSRF": "recipient-csrf",
        }),
      }),
    }),
  );
});

test("edits an empty Interest list on a Recipient's behalf and shows attributed history", async () => {
  const curator = curatorPerson(
    "11111111-1111-4111-8111-111111111111",
    "Curator",
    ["curator", "recipient"],
  );
  const recipient = curatorPerson(
    "22222222-2222-4222-8222-222222222222",
    "Recipient",
    ["recipient"],
  );
  const alex = curatorPerson("33333333-3333-4333-8333-333333333333", "Alex");
  let interest: InterestListResponse = {
    recipient,
    version: 0,
    entries: [],
    history: [],
    history_next_cursor: "older-history",
  };
  let resolveOlderHistory: (response: Response) => void = () => undefined;
  const olderHistory = new Promise<Response>((resolve) => {
    resolveOlderHistory = resolve;
  });
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/people?query=&include_archived=false") {
        return jsonResponse({ people: [curator, recipient, alex] });
      }
      if (path.startsWith("/api/visibility-circles?")) {
        return jsonResponse({ circles: [] });
      }
      if (path === `/api/interest-lists/${recipient.id}`) {
        return jsonResponse(interest);
      }
      if (path.startsWith(`/api/interest-lists/${recipient.id}?`)) {
        return olderHistory;
      }
      if (
        path.startsWith(`/api/interest-lists/${recipient.id}/discoverable?`)
      ) {
        return jsonResponse({ people: [alex] });
      }
      if (
        path === `/api/interest-lists/${recipient.id}/people/${alex.id}` &&
        init?.method === "PUT"
      ) {
        interest = {
          recipient,
          version: 1,
          entries: [
            {
              person: alex,
              state: "active",
              chosen_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            },
          ],
          history: [
            {
              id: 1,
              person: alex,
              actor: curator,
              action: "selected",
              result: "active",
              reason: "explicit",
              created_at: "2026-01-01T00:00:00Z",
            },
          ],
        };
        return jsonResponse(interest);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderManager();
  await screen.findByRole("option", { name: "Recipient" });
  fireEvent.change(screen.getByLabelText("Recipient"), {
    target: { value: recipient.id },
  });
  expect(
    await screen.findByText("No Interest list changes yet."),
  ).toBeInTheDocument();
  const alexChoice = screen.getByRole("checkbox", { name: /Alex/ });
  expect(alexChoice).not.toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Load older history" }));
  fireEvent.click(alexChoice);
  await waitFor(() => expect(alexChoice).toBeChecked());
  fireEvent.click(screen.getByText("Interest list audit history"));
  expect(await screen.findByText(/selected by Curator/)).toBeInTheDocument();
  resolveOlderHistory(
    jsonResponse({
      recipient,
      version: 0,
      entries: [],
      history: [],
    }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Load older history" }),
    ).not.toBeInTheDocument(),
  );
  expect(alexChoice).toBeChecked();
  expect(screen.getByText(/selected by Curator/)).toBeInTheDocument();
  expect(
    screen.queryByRole("option", { name: "Curator" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Interest choices" }),
  ).toBeInTheDocument();

  const mutation = requests.find(({ init }) => init?.method === "PUT");
  expect(stringBody(mutation?.init?.body)).toEqual({
    selected: true,
    version: 0,
  });
  expect(mutation?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );
});
