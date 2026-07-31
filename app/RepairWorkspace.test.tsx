import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { RepairWorkspace } from "./RepairWorkspace";
import type { ListResponse } from "./types/generated/repairs";

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
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
    throw new Error("Expected a JSON request body");
  }
  return body;
}

function renderWorkspace() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <RepairWorkspace csrfToken="private-csrf" />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("shows private normalized Media evidence and confirms only after an explicit action", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const path = requestPath(input);
    requests.push({ path, init });
    if (path.startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    if (init?.method === "POST") {
      return Promise.resolve(jsonResponse({ status: "confirmed" }));
    }
    return Promise.resolve(
      jsonResponse({
        source_problems: [
          {
            kind: "media_item",
            id: "99999999-9999-4999-8999-999999999999",
            label: "missing.jpg",
            priority: "critical",
            published: true,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 1,
          },
        ],
        person_candidates: [],
        unlinked_immich_people: [],
        media_candidates: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            media_item_id: "22222222-2222-4222-8222-222222222222",
            previous_immich_asset_id: "33333333-3333-4333-8333-333333333333",
            candidate_immich_asset_id: "44444444-4444-4444-8444-444444444444",
            media_type: "image",
            review_token:
              "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            state: "pending",
            previous: {
              checksum: "abcd",
              capture: "2026-01-01T10:00:00Z",
              filename: "old.jpg",
              path: "/private/old.jpg",
            },
            candidate: {
              checksum: "abcd",
              capture: "2026-01-01T10:00:00Z",
              filename: "new.jpg",
              path: "/private/moved/new.jpg",
            },
            face_anchors: [
              {
                face_id: "face-anchor-id",
                asset_id: "anchor-asset-id",
                checksum: "anchor-checksum",
                image_width: 1600,
                image_height: 1200,
                x1: 10,
                y1: 20,
                x2: 110,
                y2: 220,
                last_immich_person_id: "prior-person-id",
              },
            ],
            conflicts: ["checksum_matches_multiple_media"],
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("/private/old.jpg")).toBeInTheDocument();
  expect(screen.getByText("Published Media unavailable")).toBeInTheDocument();
  expect(screen.getByText("missing.jpg")).toBeInTheDocument();
  expect(screen.getByText(/highest priority/)).toBeInTheDocument();
  expect(screen.getByText("/private/moved/new.jpg")).toBeInTheDocument();
  expect(
    screen.getByText("33333333-3333-4333-8333-333333333333"),
  ).toBeInTheDocument();
  expect(
    screen.getByText("44444444-4444-4444-8444-444444444444"),
  ).toBeInTheDocument();
  expect(screen.getAllByText("abcd")).toHaveLength(2);
  expect(screen.getByText("1 related face anchor")).toBeInTheDocument();
  expect(screen.getByText("face-anchor-id")).toBeInTheDocument();
  expect(screen.getByText("anchor-asset-id")).toBeInTheDocument();
  expect(screen.getByText("anchor-checksum")).toBeInTheDocument();
  expect(screen.getByText("prior-person-id")).toBeInTheDocument();
  expect(screen.getByText("1600 × 1200")).toBeInTheDocument();
  expect(screen.getByText("10, 20 to 110, 220")).toBeInTheDocument();
  expect(
    screen.getByText("checksum matches multiple media"),
  ).toBeInTheDocument();
  expect(
    requests.filter((request) => request.init?.method === "POST"),
  ).toHaveLength(0);

  fireEvent.click(
    screen.getByRole("button", { name: "Confirm repair for new.jpg" }),
  );
  await waitFor(() => {
    const confirmation = requests.find(
      (request) => request.init?.method === "POST",
    );
    expect(confirmation?.path).toBe(
      "/api/repairs/media/11111111-1111-4111-8111-111111111111/confirm",
    );
    expect(confirmation?.init?.headers).toMatchObject({
      "Content-Type": "application/json",
      "X-Memento-CSRF": "private-csrf",
    });
    expect(stringBody(confirmation?.init?.body)).toBe(
      '{"review_token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}',
    );
  });
});

test("rejects a Media repair without sending its review token", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const path = requestPath(input);
    requests.push({ path, init });
    if (path.startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    if (init?.method === "POST") {
      return Promise.resolve(jsonResponse({ status: "rejected" }));
    }
    return Promise.resolve(
      jsonResponse({
        source_problems: [],
        person_candidates: [],
        unlinked_immich_people: [],
        media_candidates: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            media_item_id: "22222222-2222-4222-8222-222222222222",
            previous_immich_asset_id: "33333333-3333-4333-8333-333333333333",
            candidate_immich_asset_id: "44444444-4444-4444-8444-444444444444",
            media_type: "image",
            review_token:
              "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            state: "pending",
            previous: { filename: "old.jpg", path: "/private/old.jpg" },
            candidate: {
              filename: "new.jpg",
              path: "/private/moved/new.jpg",
            },
            face_anchors: [],
            conflicts: [],
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
      } satisfies ListResponse),
    );
  });

  renderWorkspace();
  fireEvent.click(
    await screen.findByRole("button", { name: "Reject repair for new.jpg" }),
  );
  await waitFor(() => {
    const rejection = requests.find(
      (request) => request.init?.method === "POST",
    );
    expect(rejection?.path).toBe(
      "/api/repairs/media/11111111-1111-4111-8111-111111111111/reject",
    );
    expect(rejection?.init?.body).toBeUndefined();
  });
});

test("explains that a missing published album does not block current Media", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
    if (requestPath(input).startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    return Promise.resolve(
      jsonResponse({
        source_problems: [
          {
            kind: "source_album",
            id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            label: "Family album",
            priority: "critical",
            published: true,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 0,
          },
        ],
        person_candidates: [],
        unlinked_immich_people: [],
        media_candidates: [],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("Source album missing")).toBeInTheDocument();
  expect(
    screen.getByText(
      /Media items remain available unless their own backing is missing/,
    ),
  ).toBeInTheDocument();
  expect(
    screen.queryByText(/Media delivery is blocked/),
  ).not.toBeInTheDocument();
});

test("renders Source problems in critical and Media-first priority order", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
    if (requestPath(input).startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    return Promise.resolve(
      jsonResponse({
        source_problems: [
          {
            kind: "media_item",
            id: "10000000-0000-4000-8000-000000000001",
            label: "Critical media",
            priority: "critical",
            published: true,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 0,
          },
          {
            kind: "source_album",
            id: "10000000-0000-4000-8000-000000000002",
            label: "Critical album",
            priority: "critical",
            published: true,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 0,
          },
          {
            kind: "media_item",
            id: "10000000-0000-4000-8000-000000000003",
            label: "High media",
            priority: "high",
            published: false,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 0,
          },
          {
            kind: "source_album",
            id: "10000000-0000-4000-8000-000000000004",
            label: "High album",
            priority: "high",
            published: false,
            missing_since: "2026-01-01T00:00:00Z",
            candidate_count: 0,
          },
        ],
        person_candidates: [],
        media_candidates: [],
        unlinked_immich_people: [],
      }),
    );
  });

  renderWorkspace();
  await screen.findByText("Critical media");
  expect(
    screen
      .getAllByRole("heading", { level: 3 })
      .map((heading) => heading.textContent),
  ).toEqual(["Critical media", "Critical album", "High media", "High album"]);
});

test("shows conflicted Person evidence and permits confirming the current link", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const path = requestPath(input);
    requests.push({ path, init });
    if (path.startsWith("/api/people")) {
      return Promise.resolve(jsonResponse({ people: [] }));
    }
    if (init?.method === "POST") {
      return Promise.resolve(jsonResponse({ status: "confirmed" }));
    }
    return Promise.resolve(
      jsonResponse({
        person_candidates: [
          {
            id: "55555555-5555-4555-8555-555555555555",
            person_id: "66666666-6666-4666-8666-666666666666",
            person_name: "Family member",
            previous_immich_person_id: "77777777-7777-4777-8777-777777777777",
            previous_immich_person_present: true,
            state: "pending",
            face_anchors: [
              {
                face_id: "person-face-id",
                asset_id: "person-asset-id",
                checksum: "person-checksum",
                image_width: 100,
                image_height: 80,
                x1: 1,
                y1: 2,
                x2: 20,
                y2: 30,
                last_immich_person_id: "77777777-7777-4777-8777-777777777777",
              },
            ],
            conflicts: ["anchors_split_across_people"],
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
        media_candidates: [],
        unlinked_immich_people: [],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("Family member")).toBeInTheDocument();
  expect(screen.getByText("person-face-id")).toBeInTheDocument();
  expect(screen.getByText("anchors split across people")).toBeInTheDocument();
  const confirmation = screen.getByRole("button", {
    name: "Confirm repair for Family member",
  });
  expect(confirmation).toHaveTextContent("Confirm current link");
  fireEvent.click(confirmation);
  await waitFor(() =>
    expect(
      requests.find((request) => request.init?.method === "POST")?.path,
    ).toBe("/api/repairs/people/55555555-5555-4555-8555-555555555555/confirm"),
  );
  expect(await screen.findByRole("status")).toHaveTextContent(
    "Repair confirmed.",
  );
});

test("selects a current portal Person when People load after repair additions", async () => {
  let resolvePeople: (response: Response) => void = () => undefined;
  const peopleResponse = new Promise<Response>((resolve) => {
    resolvePeople = resolve;
  });
  vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
    const path = requestPath(input);
    if (path.startsWith("/api/people")) {
      return peopleResponse;
    }
    return Promise.resolve(
      jsonResponse({
        person_candidates: [],
        media_candidates: [],
        unlinked_immich_people: [
          {
            immich_person_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
            name: "Late-loading cluster",
            hidden: true,
          },
        ],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("Late-loading cluster")).toBeInTheDocument();
  expect(
    screen.getByText("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
  ).toBeInTheDocument();
  expect(screen.getByText("Hidden")).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Confirm Person link" }),
  ).toBeDisabled();
  resolvePeople(
    jsonResponse({
      people: [
        {
          id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
          display_name: "Family member",
          status: "current",
        },
      ],
    }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Confirm Person link" }),
    ).toBeEnabled(),
  );
  expect(screen.getByLabelText("Portal Person")).toHaveValue(
    "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  );
});

test("keeps new Immich People as additions until the Curator selects a portal Person", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
    const path = requestPath(input);
    requests.push({ path, init });
    if (path.startsWith("/api/people")) {
      return Promise.resolve(
        jsonResponse({
          people: [
            {
              id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
              display_name: "Family member",
              status: "current",
            },
          ],
        }),
      );
    }
    if (init?.method === "POST") {
      return Promise.resolve(jsonResponse({ status: "confirmed" }));
    }
    return Promise.resolve(
      jsonResponse({
        person_candidates: [],
        media_candidates: [],
        unlinked_immich_people: [
          {
            immich_person_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
            name: "New cluster",
            hidden: false,
          },
        ],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("New cluster")).toBeInTheDocument();
  expect(
    screen.getByText(/produces no Attendance suggestions/),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Confirm Person link" }));
  await waitFor(() => {
    const request = requests.find(
      (candidate) =>
        candidate.init?.method === "POST" &&
        candidate.path === "/api/repairs/people/link",
    );
    expect(request?.init?.headers).toMatchObject({
      "X-Memento-CSRF": "private-csrf",
    });
    expect(JSON.parse(stringBody(request?.init?.body))).toEqual({
      person_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      immich_person_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    });
  });
});
