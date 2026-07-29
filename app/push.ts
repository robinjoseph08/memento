import { apiJSON, apiNoContent } from "./api";
import type {
  ConfigurationResponse,
  ReconcileRequest,
  ReconcileResponse,
  SubscriptionRequest,
} from "./types/generated/push";

export const PUSH_CONFIGURATION_QUERY_KEY = ["push-configuration"] as const;
export const PUSH_RECONCILIATION_QUERY_KEY = ["push-reconciliation"] as const;
export const PUSH_SUBSCRIPTION_CHANGED = "PUSH_SUBSCRIPTION_CHANGED";

export function supportsPushNotifications() {
  return (
    window.isSecureContext === true &&
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

export function isAppleMobileDevice(
  browserNavigator: Pick<
    Navigator,
    "maxTouchPoints" | "platform" | "userAgent"
  > = navigator,
) {
  return (
    /iPhone|iPad|iPod/i.test(browserNavigator.userAgent) ||
    (browserNavigator.platform === "MacIntel" &&
      browserNavigator.maxTouchPoints > 1)
  );
}

export function isStandaloneDisplay() {
  const standaloneNavigator = navigator as Navigator & { standalone?: boolean };
  return (
    standaloneNavigator.standalone === true ||
    window.matchMedia?.("(display-mode: standalone)").matches === true
  );
}

export function pushSubscriptionRequest(
  subscription: Pick<PushSubscription, "endpoint" | "toJSON">,
): SubscriptionRequest {
  const serialized = subscription.toJSON();
  const endpoint = serialized.endpoint ?? subscription.endpoint;
  const p256dh = serialized.keys?.p256dh;
  const auth = serialized.keys?.auth;
  if (!endpoint || !p256dh || !auth) {
    throw new Error("The browser returned an incomplete push subscription.");
  }

  let expirationTime: string | undefined;
  if (serialized.expirationTime != null) {
    const expiration = new Date(serialized.expirationTime);
    if (Number.isNaN(expiration.valueOf())) {
      throw new Error("The browser returned an invalid push expiration time.");
    }
    expirationTime = expiration.toISOString();
  }

  return {
    endpoint,
    ...(expirationTime ? { expiration_time: expirationTime } : {}),
    keys: { p256dh, auth },
  };
}

export function decodeApplicationServerKey(publicKey: string) {
  const padding = "=".repeat((4 - (publicKey.length % 4)) % 4);
  const base64 = (publicKey + padding)
    .replaceAll("-", "+")
    .replaceAll("_", "/");
  const decoded = window.atob(base64);
  return Uint8Array.from(decoded, (character) => character.charCodeAt(0));
}

export function fetchPushConfiguration() {
  return apiJSON<ConfigurationResponse>("/api/push");
}

export function enrollPushSubscription(
  subscription: PushSubscription,
  csrfToken: string,
) {
  return apiJSON<ConfigurationResponse>("/api/push", {
    method: "POST",
    headers: { "X-Memento-CSRF": csrfToken },
    body: JSON.stringify(pushSubscriptionRequest(subscription)),
  });
}

export function reconcilePushSubscription(
  subscription: PushSubscription | null,
  csrfToken: string,
) {
  const request: ReconcileRequest = subscription
    ? { subscription: pushSubscriptionRequest(subscription) }
    : {};
  return apiJSON<ReconcileResponse>("/api/push/reconcile", {
    method: "POST",
    headers: { "X-Memento-CSRF": csrfToken },
    body: JSON.stringify(request),
  });
}

export function disablePushSubscription(csrfToken: string) {
  return apiNoContent("/api/push", {
    method: "DELETE",
    headers: { "X-Memento-CSRF": csrfToken },
  });
}

export async function localPushSubscription() {
  const registration = await navigator.serviceWorker.ready;
  return registration.pushManager.getSubscription();
}

export async function reconcileAuthenticatedPush(csrfToken: string) {
  const subscription = await localPushSubscription();
  const response = await reconcilePushSubscription(subscription, csrfToken);
  if (response.remove_local && subscription) {
    try {
      await subscription.unsubscribe();
    } catch {
      // Server reconciliation is authoritative when local cleanup is blocked.
    }
  }
  return response;
}

export async function unsubscribeLocalPushBestEffort() {
  try {
    if (!("serviceWorker" in navigator) || !("PushManager" in window)) return;
    const subscription = await localPushSubscription();
    await subscription?.unsubscribe();
  } catch {
    // Local push cleanup must never block Session invalidation.
  }
}
