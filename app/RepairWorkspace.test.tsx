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
        person_candidates: [],
        unlinked_immich_people: [],
        media_candidates: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            media_item_id: "22222222-2222-4222-8222-222222222222",
            previous_immich_asset_id: "33333333-3333-4333-8333-333333333333",
            candidate_immich_asset_id: "44444444-4444-4444-8444-444444444444",
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
            face_anchors: [{ face_id: "face", asset_id: "asset" }],
            conflicts: ["checksum_matches_multiple_media"],
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
      }),
    );
  });

  renderWorkspace();
  expect(await screen.findByText("/private/old.jpg")).toBeInTheDocument();
  expect(screen.getByText("/private/moved/new.jpg")).toBeInTheDocument();
  expect(screen.getAllByText("abcd")).toHaveLength(2);
  expect(screen.getByText("1 related face anchor")).toBeInTheDocument();
  expect(
    screen.getByText("checksum matches multiple media"),
  ).toBeInTheDocument();
  expect(
    requests.filter((request) => request.init?.method === "POST"),
  ).toHaveLength(0);

  fireEvent.click(screen.getByRole("button", { name: "Confirm repair" }));
  await waitFor(() => {
    const confirmation = requests.find(
      (request) => request.init?.method === "POST",
    );
    expect(confirmation?.path).toBe(
      "/api/repairs/media/11111111-1111-4111-8111-111111111111/confirm",
    );
    expect(confirmation?.init?.headers).toMatchObject({
      "X-Memento-CSRF": "private-csrf",
    });
  });
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
