import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, expect, test, vi } from "vitest";

import { eventKeys, looseItemKeys } from "./curationKeys";
import { CURRENT_SESSION_QUERY_KEY } from "./sessions";
import {
  usePublishLooseItem,
  useUpdateLooseItem,
  useWithdrawLooseItem,
} from "./looseItems";
import type { LooseItem } from "../../types/generated/events";

const csrf = "session-generation";
const looseID = "loose-1";

function looseItem(version = 1): LooseItem {
  return {
    id: looseID,
    lifecycle: "draft",
    title: "Portrait",
    description: "Private detail",
    grouping_timezone: "UTC",
    proposed_day: "2026-08-01",
    place_labels: ["Garden"],
    version,
    audience_complete: true,
    published_editable_version: null,
    has_staged_update: false,
    pending_withdrawal_publication: false,
    withdrawal_targets: [],
    withdrawals: [],
    media_item: {
      id: "media-1",
      media_type: "image",
      width: 1200,
      height: 800,
      local_date_time: null,
    },
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  };
}

function harness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, { csrf_token: csrf });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

function json(value: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function body(init?: RequestInit) {
  if (typeof init?.body !== "string") throw new Error("Expected JSON body");
  return JSON.parse(init.body) as unknown;
}

afterEach(() => vi.unstubAllGlobals());

test("Loose metadata update sends the generated contract with CSRF and invalidates every projection", async () => {
  const { client, wrapper } = harness();
  const saved = looseItem(2);
  const fetchMock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >(() => json(saved));
  vi.stubGlobal("fetch", fetchMock);
  client.setQueryData(looseItemKeys.all(csrf), { loose_items: [] });
  client.setQueryData(looseItemKeys.detail(csrf, looseID), looseItem());
  client.setQueryData(looseItemKeys.audience(csrf, looseID), { version: 1 });
  client.setQueryData(looseItemKeys.previewRecipients(csrf, looseID), {
    recipients: [],
  });
  client.setQueryData(
    looseItemKeys.recipientPreview(csrf, looseID, 1, "person-1"),
    { authorized: false },
  );
  const onSuccess = vi.fn();
  const { result } = renderHook(
    () =>
      useUpdateLooseItem(csrf, {
        onMutate: vi.fn(),
        onSuccess,
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      looseItem: looseItem(),
      revision: 4,
      selectionGeneration: 7,
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe(`/api/loose-items/${looseID}`);
  expect(init?.method).toBe("PUT");
  expect(new Headers(init?.headers).get("X-Memento-CSRF")).toBe(csrf);
  expect(body(init)).toEqual({
    version: 1,
    title: "Portrait",
    description: "Private detail",
    grouping_timezone: "UTC",
    proposed_day: "2026-08-01",
    place_labels: ["Garden"],
  });
  expect(client.getQueryData(looseItemKeys.detail(csrf, looseID))).toEqual(
    saved,
  );
  for (const key of [
    looseItemKeys.all(csrf),
    looseItemKeys.audience(csrf, looseID),
    looseItemKeys.previewRecipients(csrf, looseID),
    looseItemKeys.recipientPreview(csrf, looseID, 1, "person-1"),
  ])
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  expect(onSuccess).toHaveBeenCalledWith(
    saved,
    expect.objectContaining({ selectionGeneration: 7 }),
  );
});

test("Loose Publication evicts Preview before request completion and returns undefined authority after uncertain recovery", async () => {
  const { client, wrapper } = harness();
  let resolvePublication!: (response: Response) => void;
  const publicationPending = new Promise<Response>((resolve) => {
    resolvePublication = resolve;
  });
  const fetchMock = vi
    .fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
    .mockReturnValueOnce(publicationPending)
    .mockResolvedValueOnce(new Response("unavailable", { status: 503 }));
  vi.stubGlobal("fetch", fetchMock);
  client.setQueryData(looseItemKeys.previewRecipients(csrf, looseID), {
    recipients: [],
  });
  client.setQueryData(
    looseItemKeys.recipientPreview(csrf, looseID, 1, "person-1"),
    { authorized: true },
  );
  client.setQueryData(eventKeys.all(csrf), { events: [] });
  client.setQueryData(eventKeys.detail(csrf, "event-1"), { id: "event-1" });
  const searchKey = ["recipient-search", csrf, { query: "portrait" }];
  client.setQueryData(searchKey, { results: [] });
  const onSuccess = vi.fn();
  const { result } = renderHook(
    () =>
      usePublishLooseItem(csrf, {
        onStarted: vi.fn(),
        onCommitted: vi.fn(),
        onSuccess,
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      looseItem: looseItem(),
      revision: 0,
      selectionGeneration: 2,
      notifyRecipients: false,
    }),
  );
  await waitFor(() => expect(result.current.isPending).toBe(true));
  expect(
    client.getQueryData(looseItemKeys.previewRecipients(csrf, looseID)),
  ).toBeUndefined();
  expect(
    client.getQueryData(
      looseItemKeys.recipientPreview(csrf, looseID, 1, "person-1"),
    ),
  ).toBeUndefined();

  act(() =>
    resolvePublication(
      new Response(
        JSON.stringify({
          id: "publication-1",
          loose_item_id: looseID,
          revision: 1,
          editable_version: 1,
          notify_recipients: false,
          committed_at: "2026-08-01T00:00:00Z",
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(onSuccess).toHaveBeenCalledWith(
    expect.objectContaining({ loose_item_id: looseID }),
    expect.objectContaining({ selectionGeneration: 2 }),
    undefined,
  );
  for (const key of [
    eventKeys.all(csrf),
    eventKeys.detail(csrf, "event-1"),
    searchKey,
  ])
    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
});

test("an old identity mutation cannot repopulate protected Loose state", async () => {
  const { client, wrapper } = harness();
  let resolveUpdate!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    resolveUpdate = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending),
  );
  client.setQueryData(looseItemKeys.detail(csrf, looseID), looseItem());
  const onSuccess = vi.fn();
  const { result } = renderHook(
    () =>
      useUpdateLooseItem(csrf, {
        onMutate: vi.fn(),
        onSuccess,
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      looseItem: looseItem(),
      revision: 1,
      selectionGeneration: 1,
    }),
  );
  await waitFor(() => expect(result.current.isPending).toBe(true));
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, {
    csrf_token: "replacement-generation",
  });
  act(() => resolveUpdate(new Response(JSON.stringify(looseItem(2)))));
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  expect(client.getQueryData(looseItemKeys.detail(csrf, looseID))).toEqual(
    looseItem(),
  );
  expect(onSuccess).not.toHaveBeenCalled();
});

test("Loose Withdrawal sends its authoritative target kind and CSRF", async () => {
  const { wrapper } = harness();
  const current = looseItem();
  current.lifecycle = "published";
  current.withdrawal_targets = [
    { target_kind: "loose_item", target_id: looseID, label: "Portrait" },
  ];
  const withdrawal = {
    id: "withdrawal-1",
    target_kind: "loose_item",
    target_id: looseID,
    reason: "Privacy correction",
    withdrawn_by_name: "Robin",
    withdrawn_at: "2026-08-01T00:00:00Z",
    restored_by_publication_id: null,
    restored_at: null,
    affected_recipient_count: 1,
    affected_media_count: 1,
    affected_event_count: 0,
  };
  const fetchMock = vi
    .fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
    .mockImplementationOnce(() => json(withdrawal, 201))
    .mockImplementationOnce(() => json({ ...current, version: 2 }));
  vi.stubGlobal("fetch", fetchMock);
  const { result } = renderHook(
    () =>
      useWithdrawLooseItem(csrf, {
        onStarted: vi.fn(),
        onCommitted: vi.fn(),
        onSuccess: vi.fn(),
        onError: vi.fn(),
      }),
    { wrapper },
  );

  act(() =>
    result.current.mutate({
      looseItem: current,
      reason: "Privacy correction",
      revision: 0,
      selectionGeneration: 1,
    }),
  );
  await waitFor(() => expect(result.current.isSuccess).toBe(true));

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe("/api/withdrawals");
  expect(init?.method).toBe("POST");
  expect(new Headers(init?.headers).get("X-Memento-CSRF")).toBe(csrf);
  expect(body(init)).toEqual({
    target_kind: "loose_item",
    target_id: looseID,
    reason: "Privacy correction",
  });
});
