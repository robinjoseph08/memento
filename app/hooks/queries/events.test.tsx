import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, expect, test, vi } from "vitest";

import { audienceKeys, useAudienceReview } from "./audiences";
import { eventKeys, useOrganizeEvent, usePublishEvent } from "./events";
import type { Review } from "../../types/generated/audiences";
import type { Event } from "../../types/generated/events";

const identityGeneration = "session-generation";
const eventID = "event-1";
const momentID = "moment-1";

function event(version: number): Event {
  return {
    id: eventID,
    lifecycle: "draft",
    title: "Gathering",
    description: "",
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
