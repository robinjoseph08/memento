import {
  focusManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
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
import { CURRENT_SESSION_QUERY_KEY } from "./hooks/queries/sessions";
import type { Person as CuratorPerson } from "./types/generated/people";
import type {
  Circle,
  CircleRequest,
  InterestListResponse,
  InterestMutationRequest,
  PeopleSearchRequest,
  MembershipRequest,
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

function requestBody<T>(body: BodyInit | null | undefined): T {
  if (typeof body !== "string") throw new Error("Expected JSON body");
  return JSON.parse(body) as T;
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
          onboarding_required: false,
        }}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  focusManager.setFocused(undefined);
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
          name: requestBody<CircleRequest>(init.body).name,
          version: 1,
          members: [],
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        };
        circles = [...circles, created];
        return jsonResponse(created, 201);
      }
      if (path.includes("/members/") && init?.method === "PUT") {
        const included = requestBody<MembershipRequest>(init.body).included;
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
        const body = requestBody<CircleRequest>(init.body);
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
  expect(requestBody<MembershipRequest>(membershipRequest?.init?.body)).toEqual(
    {
      included: true,
      version: 1,
    },
  );
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
  expect(requestBody<CircleRequest>(updateRequest?.init?.body)).toEqual({
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
  expect(requestBody<CircleRequest>(createRequest?.init?.body)).toEqual({
    name: "Grandparents",
  });
}, 10_000);

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
  let directoryAvailable = true;
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
      if (path === "/api/me/people/search" && init?.method === "POST") {
        const search = requestBody<PeopleSearchRequest>(init.body);
        if (!directoryAvailable || search.query === "nobody") {
          return jsonResponse({ people: [], next_cursor: null });
        }
        return search.cursor === nextCursor
          ? jsonResponse({ people: [blair], next_cursor: null })
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
  const session = {
    display_name: "Recipient",
    session_type: "trusted",
    csrf_token: "recipient-csrf",
    curator: false,
    onboarding_required: false,
  };
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, session);
  render(
    <QueryClientProvider client={client}>
      <RecipientVisibilityManager onSignOut={onSignOut} session={session} />
    </QueryClientProvider>,
  );

  const searchbox = screen.getByRole("searchbox", {
    name: "Search People available for your Interest list",
  });
  expect(searchbox).toHaveAttribute("maxlength", "200");
  let alexChoice = await screen.findByRole("checkbox", { name: /Alex/ });
  expect(
    screen.queryByRole("checkbox", { name: /Blair/ }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("sibling")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Load more People" }));
  expect(await screen.findByRole("checkbox", { name: /Blair/ })).toBeVisible();
  const pageRequests = requests.filter(
    ({ path, init }) =>
      path === "/api/me/people/search" && init?.method === "POST",
  );
  expect(pageRequests).toHaveLength(2);
  expect(requestBody<PeopleSearchRequest>(pageRequests[0]?.init?.body)).toEqual(
    {
      query: "",
      limit: 25,
    },
  );
  expect(requestBody<PeopleSearchRequest>(pageRequests[1]?.init?.body)).toEqual(
    {
      query: "",
      cursor: nextCursor,
      limit: 25,
    },
  );
  expect(pageRequests.every(({ path }) => !path.includes("?"))).toBe(true);
  for (const request of pageRequests) {
    expect(new Headers(request.init?.headers).has("X-Memento-CSRF")).toBe(
      false,
    );
  }

  directoryAvailable = false;
  focusManager.setFocused(false);
  focusManager.setFocused(true);
  await waitFor(() =>
    expect(
      screen.queryByRole("checkbox", { name: /Blair/ }),
    ).not.toBeInTheDocument(),
  );
  expect(
    screen.getByText("No People are available for this search."),
  ).toBeVisible();
  directoryAvailable = true;
  focusManager.setFocused(false);
  focusManager.setFocused(true);
  alexChoice = await screen.findByRole("checkbox", { name: /Alex/ });

  fireEvent.change(searchbox, { target: { value: "nobody" } });
  fireEvent.submit(screen.getByRole("search"));
  expect(
    await screen.findByText("No People are available for this search."),
  ).toBeVisible();
  const searchRequest = requests.find(({ init }) => {
    if (init?.method !== "POST" || !init.body) return false;
    return requestBody<PeopleSearchRequest>(init.body).query === "nobody";
  });
  expect(searchRequest?.path).toBe("/api/me/people/search");
  fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
  alexChoice = await screen.findByRole("checkbox", { name: /Alex/ });
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
  expect(requestBody<InterestMutationRequest>(mutation?.init?.body)).toEqual({
    selected: true,
    version: 0,
  });
  expect(mutation?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "recipient-csrf" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  await waitFor(() => expect(onSignOut).toHaveBeenCalledOnce());
  const logout = requests.find(({ path }) => path === "/api/session/logout");
  expect(logout?.init?.method).toBe("POST");
  expect(logout?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "recipient-csrf" }),
  );
});

test("aborts an in-flight private directory query when its account surface unmounts", async () => {
  let searchSignal: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      if (path === "/api/me/people/search") {
        searchSignal = init?.signal ?? undefined;
        return new Promise<Response>(() => undefined);
      }
      if (path === "/api/me/interest-list") {
        return jsonResponse({
          recipient: {
            id: "22222222-2222-4222-8222-222222222222",
            display_name: "Recipient",
            sort_name: "Recipient",
          },
          version: 0,
          entries: [],
          history: [],
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const session = {
    display_name: "Recipient",
    session_type: "trusted",
    csrf_token: "private-query-session",
    curator: false,
    onboarding_required: false,
  };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, session);
  const { unmount } = render(
    <QueryClientProvider client={client}>
      <RecipientVisibilityManager onSignOut={vi.fn()} session={session} />
    </QueryClientProvider>,
  );

  await waitFor(() => expect(searchSignal).toBeDefined());
  unmount();
  expect(searchSignal?.aborted).toBe(true);
});

test("ignores a delayed Interest mutation after the account surface remounts", async () => {
  const recipient = curatorPerson(
    "22222222-2222-4222-8222-222222222222",
    "Recipient",
    ["recipient"],
  );
  const alex = {
    id: "33333333-3333-4333-8333-333333333333",
    display_name: "Alex",
    sort_name: "Alex",
  };
  let resolveMutation: (response: Response) => void = () => undefined;
  const delayedMutation = new Promise<Response>((resolve) => {
    resolveMutation = resolve;
  });
  let mutationRequests = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      if (path === "/api/me/people/search") {
        return jsonResponse({ people: [], next_cursor: null });
      }
      if (path === "/api/me/interest-list") {
        return jsonResponse({
          recipient,
          version: 0,
          entries: [
            {
              person: alex,
              state: "ineligible",
              chosen_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            },
          ],
          history: [],
        });
      }
      if (
        path === `/api/me/interest-list/${alex.id}` &&
        init?.method === "PUT"
      ) {
        mutationRequests++;
        return delayedMutation;
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const session = {
    display_name: "Recipient",
    session_type: "trusted",
    csrf_token: "same-session",
    curator: false,
    onboarding_required: false,
  };
  const interestKey = ["recipient-interest-list", session.csrf_token] as const;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, session);
  const view = render(
    <QueryClientProvider client={client}>
      <RecipientVisibilityManager onSignOut={vi.fn()} session={session} />
    </QueryClientProvider>,
  );

  const retainedChoice = await screen.findByRole("checkbox", { name: "Alex" });
  expect(retainedChoice).toBeEnabled();
  expect(
    screen.getByText(
      "Inactive after visibility loss. Select again to check current availability.",
    ),
  ).toBeVisible();
  fireEvent.click(retainedChoice);
  await waitFor(() => expect(mutationRequests).toBe(1));
  view.unmount();
  render(
    <QueryClientProvider client={client}>
      <RecipientVisibilityManager onSignOut={vi.fn()} session={session} />
    </QueryClientProvider>,
  );
  await screen.findByText(
    "Inactive after visibility loss. Select again to check current availability.",
  );
  resolveMutation(
    jsonResponse({
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
      history: [],
    }),
  );
  await waitFor(() =>
    expect(
      client
        .getMutationCache()
        .getAll()
        .some((mutation) => mutation.state.status === "success"),
    ).toBe(true),
  );
  const retained = client.getQueryData<InterestListResponse>(interestKey);
  expect(retained?.version).toBe(0);
  expect(retained?.entries[0]?.state).toBe("ineligible");
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
  expect(requestBody<InterestMutationRequest>(mutation?.init?.body)).toEqual({
    selected: true,
    version: 0,
  });
  expect(mutation?.init?.headers).toEqual(
    expect.objectContaining({ "X-Memento-CSRF": "csrf-token" }),
  );
});
