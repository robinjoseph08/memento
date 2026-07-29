import { afterEach, expect, test, vi } from "vitest";

import {
  pushSubscriptionRequest,
  reconcileAuthenticatedPush,
  unsubscribeLocalPushBestEffort,
} from "./push";

function subscription(
  overrides?: Partial<PushSubscriptionJSON>,
  unsubscribe = vi.fn(() => Promise.resolve(true)),
) {
  return {
    endpoint: "https://push.example/subscription",
    toJSON: () => ({
      endpoint: "https://push.example/subscription",
      expirationTime: 1_893_456_000_000,
      keys: { p256dh: "p256dh-value", auth: "auth-value" },
      ...overrides,
    }),
    unsubscribe,
  } as unknown as PushSubscription;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
});

test("converts PushSubscription JSON to the API request shape", () => {
  expect(pushSubscriptionRequest(subscription())).toEqual({
    endpoint: "https://push.example/subscription",
    expiration_time: "2030-01-01T00:00:00.000Z",
    keys: { p256dh: "p256dh-value", auth: "auth-value" },
  });
});

test("authenticated reconciliation removes an orphan locally without subscribing", async () => {
  const unsubscribe = vi.fn(() => Promise.resolve(true));
  const local = subscription({ expirationTime: null }, unsubscribe);
  const getSubscription = vi.fn(() => Promise.resolve(local));
  const subscribe = vi.fn();
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      ready: Promise.resolve({ pushManager: { getSubscription, subscribe } }),
    },
  });
  const fetchMock = vi.fn(() =>
    Promise.resolve(
      new Response(JSON.stringify({ enrolled: false, remove_local: true }), {
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);

  await expect(reconcileAuthenticatedPush("csrf-token")).resolves.toEqual({
    enrolled: false,
    remove_local: true,
  });

  expect(getSubscription).toHaveBeenCalledOnce();
  expect(subscribe).not.toHaveBeenCalled();
  expect(unsubscribe).toHaveBeenCalledOnce();
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/push/reconcile",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({
        subscription: {
          endpoint: "https://push.example/subscription",
          keys: { p256dh: "p256dh-value", auth: "auth-value" },
        },
      }),
    }),
  );
});

test("best-effort local cleanup absorbs browser failures", async () => {
  vi.stubGlobal("PushManager", class PushManager {});
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: { ready: Promise.reject(new Error("browser cleanup blocked")) },
  });

  await expect(unsubscribeLocalPushBestEffort()).resolves.toBeUndefined();
});
