import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, expect, test, vi } from "vitest";

import { audienceKeys, useAudienceReview } from "./audiences";
import {
  eventKeys,
  useCreateEventDraft,
  useCreateLooseItem,
  useOrganizeEvent,
  usePublishEvent,
  useWithdrawEvent,
} from "./events";
import { sourceKeys } from "./curationKeys";
import { CURRENT_SESSION_QUERY_KEY } from "./sessions";
import type { Review } from "../../types/generated/audiences";
import type { Event } from "../../types/generated/events";

const identityGeneration = "session-generation";
const eventID = "event-1";
const momentID = "moment-1";

function jsonBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected a JSON body");
  return JSON.parse(body) as unknown;
}

function event(version: number): Event {
  return {
    id: eventID,
    lifecycle: "draft",
    title: "Gathering",
    description: "",
    date_start: null,
    date_end: null,
    selected_cover_media_item_id: null,
    place_labels: [],
    grouping_timezone: "UTC",
    version,
    final_review_complete: false,
    published_editable_version: null,
    published_attendance_recovery_required: false,
    staged_update: null,
    sources: [],
    moments: [],
    unassigned_media: [],
    withdrawal_targets: [],
    withdrawals: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    pending_withdrawal_publication: false,
  };
}

function review(version: number): Review {
  return {
    target_kind: "moment",
    target_id: momentID,
    version,
    attendance_confirmed: version > 1,
    audience_complete: false,
    people: [],
    eligible_recipients: [],
    attendance: [],
    face_evidence: [],
    face_evidence_available: false,
    proposal: [],
    approved_audience: null,
  };
}

function queryHarness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

afterEach(() => vi.unstubAllGlobals());

test("Event draft creation seeds its detail and invalidates Event and Source projections", async () => {
  const { client, wrapper } = queryHarness();
  const created = event(1);
  const fetchMock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >(() =>
    Promise.resolve(
      new Response(JSON.stringify(created), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, {
    display_name: "Robin",
    session_type: "trusted",
    csrf_token: identityGeneration,
    curator: true,
    onboarding_required: false,
  });
  client.setQueryData(eventKeys.all(identityGeneration), { events: [] });
  client.setQueryData(sourceKeys.all(identityGeneration), { albums: [] });
  const { result } = renderHook(() => useCreateEventDraft(identityGeneration), {
    wrapper,
  });

  act(() =>
    result.current.mutate({
      source_album_ids: ["source-1", "source-2"],
      media_item_ids: ["media-1"],
      timezone: "America/New_York",
      title: "Gathering",
      description: "A private draft",
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(fetchMock).toHaveBeenCalledOnce();
  const [requestURL, requestInit] = fetchMock.mock.calls[0];
  expect(requestURL).toBe("/api/events");
  expect(requestInit?.method).toBe("POST");
  expect(new Headers(requestInit?.headers).get("X-Memento-CSRF")).toBe(
    identityGeneration,
  );
  expect(jsonBody(requestInit?.body)).toEqual({
    source_album_ids: ["source-1", "source-2"],
    media_item_ids: ["media-1"],
    timezone: "America/New_York",
    title: "Gathering",
    description: "A private draft",
  });
  expect(
    client.getQueryData(eventKeys.detail(identityGeneration, eventID)),
  ).toEqual(created);
  expect(
    client.getQueryState(eventKeys.all(identityGeneration))?.isInvalidated,
  ).toBe(true);
  expect(
    client.getQueryState(sourceKeys.all(identityGeneration))?.isInvalidated,
  ).toBe(true);
});

test("Event draft creation cannot repopulate cache after identity changes", async () => {
  const { client, wrapper } = queryHarness();
  const fetchMock = vi.fn(() =>
    Promise.resolve(
      new Response(JSON.stringify(event(1)), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  const { result } = renderHook(() => useCreateEventDraft(identityGeneration), {
    wrapper,
  });

  act(() =>
    result.current.mutate({
      source_album_ids: ["source-1"],
      timezone: "UTC",
      title: "Gathering",
      description: "",
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(
    client.getQueryData(eventKeys.detail(identityGeneration, eventID)),
  ).toBeUndefined();
});

test("Loose item creation sends one stable Media identity", async () => {
  const { wrapper } = queryHarness();
  const looseItem = {
    id: "loose-1",
    lifecycle: "draft",
    title: "Portrait",
    description: "",
    grouping_timezone: "UTC",
    proposed_day: null,
    version: 1,
    audience_complete: false,
    media_item: {
      id: "media-1",
      media_type: "image",
      width: 100,
      height: 100,
      local_date_time: null,
    },
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  const fetchMock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >(() =>
    Promise.resolve(
      new Response(JSON.stringify(looseItem), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  const { result } = renderHook(() => useCreateLooseItem(identityGeneration), {
    wrapper,
  });

  act(() =>
    result.current.mutate({
      media_item_id: "media-1",
      timezone: "UTC",
      title: "Portrait",
      description: "",
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(fetchMock).toHaveBeenCalledOnce();
  const [requestURL, requestInit] = fetchMock.mock.calls[0];
  expect(requestURL).toBe("/api/loose-items");
  expect(requestInit?.method).toBe("POST");
  expect(new Headers(requestInit?.headers).get("X-Memento-CSRF")).toBe(
    identityGeneration,
  );
  expect(jsonBody(requestInit?.body)).toEqual({
    media_item_id: "media-1",
    timezone: "UTC",
    title: "Portrait",
    description: "",
  });
});

test("organization owns authoritative Event cache updates and projection invalidation", async () => {
  const { client, wrapper } = queryHarness();
  const saved = event(2);
  const fetchMock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >(() =>
    Promise.resolve(
      new Response(JSON.stringify(saved), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  client.setQueryData(eventKeys.detail(identityGeneration, eventID), event(1));
  client.setQueryData(eventKeys.all(identityGeneration), { events: [] });
  client.setQueryData(
    audienceKeys.review(identityGeneration, momentID),
    review(1),
  );
  client.setQueryData(
    eventKeys.previewRecipients(identityGeneration, eventID),
    { recipients: [] },
  );
  client.setQueryData(
    eventKeys.recipientPreview(identityGeneration, eventID, 1, "recipient-1"),
    { authorized: false },
  );
  const onSuccess = vi.fn();
  const { result } = renderHook(
    () =>
      useOrganizeEvent(identityGeneration, {
        onMutate: vi.fn(),
        onSuccess,
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      event: event(1),
      revision: 4,
      selectionGeneration: 0,
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(fetchMock).toHaveBeenCalledOnce();
  const [requestURL, requestInit] = fetchMock.mock.calls[0];
  expect(requestURL).toBe(`/api/events/${eventID}/organization`);
  expect(requestInit?.method).toBe("PUT");
  expect(new Headers(requestInit?.headers).get("X-Memento-CSRF")).toBe(
    identityGeneration,
  );
  expect(jsonBody(requestInit?.body)).toEqual({
    version: 1,
    title: "Gathering",
    description: "",
    date_start: null,
    date_end: null,
    selected_cover_media_item_id: null,
    place_labels: [],
    grouping_timezone: "UTC",
    moments: [],
    unassigned_media_ids: [],
    final_review_complete: false,
  });
  expect(
    client.getQueryData(eventKeys.detail(identityGeneration, eventID)),
  ).toEqual(saved);
  expect(
    client.getQueryState(eventKeys.all(identityGeneration))?.isInvalidated,
  ).toBe(true);
  expect(
    client.getQueryState(audienceKeys.review(identityGeneration, momentID))
      ?.isInvalidated,
  ).toBe(true);
  expect(
    client.getQueryState(
      eventKeys.previewRecipients(identityGeneration, eventID),
    )?.isInvalidated,
  ).toBe(true);
  expect(
    client.getQueryState(
      eventKeys.recipientPreview(identityGeneration, eventID, 1, "recipient-1"),
    )?.isInvalidated,
  ).toBe(true);
  expect(onSuccess).toHaveBeenCalledOnce();
  expect(onSuccess.mock.calls[0]?.[1]).toMatchObject({ revision: 4 });
});

test("organization cannot replace a newer cached Event version", async () => {
  const { client, wrapper } = queryHarness();
  const newer = event(3);
  newer.final_review_complete = true;
  client.setQueryData(eventKeys.detail(identityGeneration, eventID), newer);
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(event(2)), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );
  const { result } = renderHook(
    () =>
      useOrganizeEvent(identityGeneration, {
        onMutate: vi.fn(),
        onSuccess: () => undefined,
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      event: event(1),
      revision: 1,
      selectionGeneration: 0,
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(
    client.getQueryData(eventKeys.detail(identityGeneration, eventID)),
  ).toEqual(newer);
});

test("Publication invalidates every Event detail projection", async () => {
  const { client, wrapper } = queryHarness();
  const published = event(2);
  published.lifecycle = "published";
  published.published_editable_version = 2;
  const otherEvent = { ...event(4), id: "event-2" };
  client.setQueryData(eventKeys.detail(identityGeneration, eventID), event(1));
  client.setQueryData(
    eventKeys.detail(identityGeneration, otherEvent.id),
    otherEvent,
  );
  const recipientLibraryKey = [
    "recipient-library",
    identityGeneration,
    "photos",
    "chronology",
  ];
  const recipientEventsKey = ["recipient-events", identityGeneration];
  const recipientEventKey = ["recipient-event", identityGeneration, eventID];
  const newForYouKey = ["new-for-you", identityGeneration];
  const recipientSearchKey = [
    "recipient-search",
    identityGeneration,
    { query: "Gathering" },
  ];
  for (const key of [
    recipientLibraryKey,
    recipientEventsKey,
    recipientEventKey,
    newForYouKey,
    recipientSearchKey,
  ])
    client.setQueryData(key, { private: true });
  const publication = {
    id: "publication-1",
    event_id: eventID,
    revision: 1,
    editable_version: 2,
    notify_recipients: true,
    committed_at: "2026-01-01T00:00:00Z",
  };
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(publication), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(published), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
  );
  const { result } = renderHook(
    () =>
      usePublishEvent(identityGeneration, {
        onStarted: vi.fn(),
        onCommitted: vi.fn(),
        onSuccess: vi.fn(),
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      event: event(1),
      revision: 0,
      selectionGeneration: 0,
      notifyRecipients: true,
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(
    client.getQueryState(eventKeys.detail(identityGeneration, otherEvent.id))
      ?.isInvalidated,
  ).toBe(true);
  for (const key of [
    recipientLibraryKey,
    recipientEventsKey,
    recipientEventKey,
    newForYouKey,
    recipientSearchKey,
  ])
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
});

test("Withdrawal evicts and invalidates every protected Recipient projection", async () => {
  const { client, wrapper } = queryHarness();
  const withdrawn = event(2);
  withdrawn.lifecycle = "published";
  const protectedKeys = [
    ["recipient-library", identityGeneration, "photos", "chronology"],
    ["recipient-events", identityGeneration],
    ["recipient-event", identityGeneration, eventID],
    ["recipient-event", identityGeneration, "reused-event"],
    ["new-for-you", identityGeneration],
    ["recipient-search", identityGeneration, { query: "Gathering" }],
  ];
  for (const key of protectedKeys) client.setQueryData(key, { private: true });
  const withdrawal = {
    id: "withdrawal-1",
    target_kind: "event",
    target_id: eventID,
    reason: "Privacy correction",
    withdrawn_at: "2026-01-01T00:00:00Z",
  };
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(withdrawal), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(withdrawn), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
  );
  const { result } = renderHook(
    () =>
      useWithdrawEvent(identityGeneration, {
        onStarted: () => {
          for (const key of protectedKeys)
            expect(client.getQueryData(key)).toBeUndefined();
        },
        onCommitted: vi.fn(),
        onSuccess: vi.fn(),
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      event: event(1),
      revision: 0,
      selectionGeneration: 0,
      target: { target_kind: "event", target_id: eventID, label: "Event" },
      reason: "Privacy correction",
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  for (const key of protectedKeys)
    expect(client.getQueryState(key)).toBeUndefined();
});

test("Audience mutations update their review and invalidate Event projections", async () => {
  const { client, wrapper } = queryHarness();
  const initial = review(1);
  const confirmed = review(2);
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(JSON.stringify(initial), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    )
    .mockResolvedValueOnce(
      new Response(JSON.stringify(confirmed), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
  vi.stubGlobal("fetch", fetchMock);
  client.setQueryData(eventKeys.all(identityGeneration), { events: [] });
  client.setQueryData(eventKeys.detail(identityGeneration, eventID), event(1));
  client.setQueryData(
    eventKeys.previewRecipients(identityGeneration, eventID),
    { recipients: [] },
  );
  const onAttendanceConfirmed = vi.fn();
  const { result } = renderHook(
    () =>
      useAudienceReview(identityGeneration, momentID, {
        onAttendanceConfirmed,
        onAudienceChanged: vi.fn(),
        onAudienceApproved: vi.fn(),
      }),
    { wrapper },
  );
  await waitFor(() => expect(result.current.review.isSuccess).toBe(true));

  act(() => result.current.confirmAttendance.mutate({ person_ids: [] }));
  await waitFor(() =>
    expect(result.current.confirmAttendance.isSuccess).toBe(true),
  );

  expect(
    client.getQueryData(audienceKeys.review(identityGeneration, momentID)),
  ).toEqual(confirmed);
  expect(
    client.getQueryState(eventKeys.detail(identityGeneration, eventID))
      ?.isInvalidated,
  ).toBe(true);
  expect(
    client.getQueryState(
      eventKeys.previewRecipients(identityGeneration, eventID),
    )?.isInvalidated,
  ).toBe(true);
  expect(onAttendanceConfirmed).toHaveBeenCalledOnce();
});
