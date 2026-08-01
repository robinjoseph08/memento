import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { SourceDraftBuilder } from "./SourceDrafting";
import { CURRENT_SESSION_QUERY_KEY } from "./hooks/queries/sessions";
import { problemResponse } from "./test/problem";
import type { Event, LooseItem } from "./types/generated/events";
import type { Album } from "./types/generated/sources";

const csrfToken = "session-generation";

function album(id: string, name: string): Album {
  return {
    id,
    name,
    description: "",
    asset_count: 2,
    source_created_at: "2026-01-01T00:00:00Z",
    source_updated_at: "2026-01-01T00:00:00Z",
    start_at: null,
    end_at: null,
    disposition: "unreviewed",
    version: 1,
    first_seen_at: "2026-01-01T00:00:00Z",
    last_seen_at: "2026-01-01T00:00:00Z",
    source_missing: false,
  };
}

const mediaShared = {
  id: "media-shared",
  media_type: "image",
  width: 1200,
  height: 800,
  local_date_time: "2026-06-01T12:00:00",
};
const mediaUndated = {
  id: "media-undated",
  media_type: "video",
  width: null,
  height: null,
  local_date_time: null,
};
const mediaUnused = {
  id: "media-unused",
  media_type: "image",
  width: 800,
  height: 800,
  local_date_time: "2026-06-02T09:00:00+99:99",
};

function createdEvent(): Event {
  return {
    id: "event-created",
    lifecycle: "draft",
    title: "Combined trip",
    description: "",
    place_labels: [],
    grouping_timezone: "UTC",
    version: 1,
    final_review_complete: false,
    published_editable_version: null,
    published_attendance_recovery_required: false,
    staged_update: null,
    sources: [],
    moments: [],
    unassigned_media: [mediaUndated],
    withdrawal_targets: [],
    withdrawals: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    pending_withdrawal_publication: false,
  };
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.pathname;
  return new URL(input.url).pathname;
}

function jsonBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected a JSON body");
  const parsed: unknown = JSON.parse(body);
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
    throw new Error("Expected a JSON object body");
  return parsed as Record<string, unknown>;
}

function renderBuilder(
  albums = [album("source-1", "Trip"), album("source-2", "Reunion")],
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, {
    display_name: "Robin",
    session_type: "trusted",
    csrf_token: csrfToken,
    curator: true,
    onboarding_required: false,
  });
  return render(
    <QueryClientProvider client={client}>
      <BrowserRouter>
        <SourceDraftBuilder
          albums={albums}
          csrfToken={csrfToken}
          onClose={vi.fn()}
        />
      </BrowserRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("combines Sources, deduplicates stable Media, and creates an explicit private Event subset", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/sources/source-1/media-items")
        return Promise.resolve(
          new Response(
            JSON.stringify({ media_items: [mediaShared, mediaUnused] }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          ),
        );
      if (path === "/api/sources/source-2/media-items")
        return Promise.resolve(
          new Response(
            JSON.stringify({ media_items: [mediaShared, mediaUndated] }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          ),
        );
      if (path === "/api/events")
        return Promise.resolve(
          new Response(JSON.stringify(createdEvent()), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        );
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );
  renderBuilder();

  expect(await screen.findAllByText("Trip")).not.toHaveLength(0);
  expect(screen.getAllByText("Reunion")).not.toHaveLength(0);
  fireEvent.click(screen.getByLabelText("Choose current Media"));
  expect(await screen.findAllByText(/Media media-shared/)).toHaveLength(1);
  expect(
    screen.getByLabelText(/Select undated photo Media media-unused/),
  ).not.toBeChecked();
  fireEvent.click(screen.getByLabelText(/Select photo Media media-shared/));
  fireEvent.click(
    screen.getByLabelText(/Select undated video Media media-undated/),
  );
  expect(
    screen.getByText("1 item will remain private and unused."),
  ).toBeVisible();
  fireEvent.change(screen.getByLabelText("Grouping timezone"), {
    target: { value: "UTC" },
  });
  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: "Combined trip" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Create private Event draft" }),
  );

  await waitFor(() =>
    expect(window.location.search).toBe(
      "?workspace=drafts&event=event-created",
    ),
  );
  const creation = requests.find(({ path }) => path === "/api/events");
  expect(creation?.init?.method).toBe("POST");
  expect(new Headers(creation?.init?.headers).get("X-Memento-CSRF")).toBe(
    csrfToken,
  );
  const { idempotency_key: idempotencyKey, ...eventRequest } = jsonBody(
    creation?.init?.body,
  );
  expect(typeof idempotencyKey).toBe("string");
  expect(eventRequest).toEqual({
    source_album_ids: ["source-1", "source-2"],
    media_item_ids: ["media-shared", "media-undated"],
    timezone: "UTC",
    title: "Combined trip",
    description: "",
  });
});

test("drafts every current and future Source Media when no subset is chosen", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path === "/api/sources/source-1/media-items")
        return Promise.resolve(
          new Response(JSON.stringify({ media_items: [mediaShared] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      if (path === "/api/events")
        return Promise.resolve(
          new Response(JSON.stringify(createdEvent()), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        );
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );
  renderBuilder([album("source-1", "Trip")]);

  expect(
    screen.getByLabelText("All current and future Source Media"),
  ).toBeChecked();
  fireEvent.change(screen.getByLabelText("Grouping timezone"), {
    target: { value: "UTC" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Create private Event draft" }),
  );

  await waitFor(() =>
    expect(window.location.search).toBe(
      "?workspace=drafts&event=event-created",
    ),
  );
  const creation = requests.find(({ path }) => path === "/api/events");
  const { idempotency_key: idempotencyKey, ...eventRequest } = jsonBody(
    creation?.init?.body,
  );
  expect(typeof idempotencyKey).toBe("string");
  expect(eventRequest).toEqual({
    source_album_ids: ["source-1"],
    timezone: "UTC",
    title: "",
    description: "",
  });
});

test("renders large Source selections in bounded Media batches", async () => {
  const manyMedia = Array.from({ length: 201 }, (_, index) => ({
    id: `bulk-media-${index}`,
    media_type: "image",
    width: 100,
    height: 100,
    local_date_time: null,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/sources/source-1/media-items")
        return Promise.resolve(
          new Response(JSON.stringify({ media_items: manyMedia }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );
  renderBuilder([album("source-1", "Trip")]);

  fireEvent.click(screen.getByLabelText("Choose current Media"));
  await waitFor(() =>
    expect(
      screen.getAllByLabelText(/Select undated photo Media bulk-media-/),
    ).toHaveLength(200),
  );
  expect(
    screen.getByText("Showing 200 of 201 available Media items."),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Load more Media" }));
  expect(
    screen.getAllByLabelText(/Select undated photo Media bulk-media-/),
  ).toHaveLength(201);
});

test("refreshes stale Media after a creation conflict without losing draft details", async () => {
  let mediaLoads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path === "/api/sources/source-1/media-items") {
        mediaLoads += 1;
        return Promise.resolve(
          new Response(
            JSON.stringify({
              media_items:
                mediaLoads === 1 ? [mediaShared, mediaUndated] : [mediaShared],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (path === "/api/events")
        return Promise.resolve(
          new Response(
            JSON.stringify(
              problemResponse(
                "Source Media changed. Review the available Media and retry.",
                409,
                "conflict",
              ),
            ),
            { status: 409, headers: { "Content-Type": "application/json" } },
          ),
        );
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );
  renderBuilder([album("source-1", "Trip")]);

  fireEvent.click(screen.getByLabelText("Choose current Media"));
  fireEvent.click(
    await screen.findByLabelText(/Select undated video Media media-undated/),
  );
  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: "Keep this draft title" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Create private Event draft" }),
  );

  expect(
    await screen.findByText(
      "Source Media changed. Review the available Media and retry.",
    ),
  ).toBeVisible();
  await waitFor(() => expect(mediaLoads).toBe(2));
  expect(
    screen.queryByLabelText(/Select undated video Media media-undated/),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("Event title")).toHaveValue(
    "Keep this draft title",
  );
  expect(
    screen.getByRole("button", { name: "Create private Event draft" }),
  ).toBeDisabled();
});

test("creates a Loose item around one stable Media identity and preserves Source context", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  const loose: LooseItem = {
    id: "loose-created",
    lifecycle: "draft",
    title: "",
    description: "",
    grouping_timezone: "UTC",
    proposed_day: null,
    version: 1,
    audience_complete: false,
    media_item: mediaUndated,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = requestPath(input);
      requests.push({ path, init });
      if (path.endsWith("/media-items"))
        return Promise.resolve(
          new Response(JSON.stringify({ media_items: [mediaUndated] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      if (path === "/api/loose-items")
        return Promise.resolve(
          new Response(JSON.stringify(loose), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        );
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );
  renderBuilder([album("source-1", "Trip")]);

  fireEvent.click(screen.getByLabelText("Loose item"));
  fireEvent.click(
    await screen.findByLabelText(/Select undated video Media media-undated/),
  );
  fireEvent.change(screen.getByLabelText("Grouping timezone"), {
    target: { value: "UTC" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Create private Loose item" }),
  );

  expect(
    await screen.findByText(
      "Loose item is ready privately. Review its Audience before Publication.",
    ),
  ).toBeVisible();
  expect(screen.getAllByText("Trip")).not.toHaveLength(0);
  expect(
    screen.getByLabelText(/Select undated video Media media-undated/),
  ).not.toBeChecked();
  const creation = requests.find(({ path }) => path === "/api/loose-items");
  expect(jsonBody(creation?.init?.body)).toEqual({
    media_item_id: "media-undated",
    timezone: "UTC",
    title: "",
    description: "",
  });
});
