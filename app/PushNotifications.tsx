import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import type { SessionResponse } from "./types/generated/setup";
import {
  PUSH_CONFIGURATION_QUERY_KEY,
  PUSH_RECONCILIATION_QUERY_KEY,
  PUSH_SUBSCRIPTION_CHANGED,
  decodeApplicationServerKey,
  disablePushSubscription,
  enrollPushSubscription,
  fetchPushConfiguration,
  isAppleMobileDevice,
  isStandaloneDisplay,
  reconcileAuthenticatedPush,
  supportsPushNotifications,
  unsubscribeLocalPushBestEffort,
} from "./push";
import type { ReconcileResponse } from "./types/generated/push";

export function PushNotifications({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const isPublic = session.session_type === "public";
  const supported = !isPublic && supportsPushNotifications();
  const needsAppleInstallation =
    !isPublic && isAppleMobileDevice() && !isStandaloneDisplay();
  const [permissionResult, setPermissionResult] =
    useState<NotificationPermission>();
  const configuration = useQuery({
    queryKey: PUSH_CONFIGURATION_QUERY_KEY,
    queryFn: fetchPushConfiguration,
    enabled: !isPublic,
    retry: false,
  });
  const canReconcile =
    supported &&
    !needsAppleInstallation &&
    configuration.data?.available === true;
  const reconciliation = useQuery({
    queryKey: PUSH_RECONCILIATION_QUERY_KEY,
    queryFn: () => reconcileAuthenticatedPush(session.csrf_token),
    enabled: canReconcile,
    retry: false,
  });

  useEffect(() => {
    if (!canReconcile) return;
    const reconcileAfterSubscriptionChange = (event: MessageEvent) => {
      const message: unknown = event.data;
      if (
        typeof message === "object" &&
        message !== null &&
        "type" in message &&
        message.type === PUSH_SUBSCRIPTION_CHANGED
      ) {
        void reconciliation.refetch();
      }
    };
    navigator.serviceWorker.addEventListener(
      "message",
      reconcileAfterSubscriptionChange,
    );
    return () =>
      navigator.serviceWorker.removeEventListener(
        "message",
        reconcileAfterSubscriptionChange,
      );
  }, [canReconcile, reconciliation]);

  const enable = useMutation({
    mutationFn: async (permissionRequest: Promise<NotificationPermission>) => {
      const permission = await permissionRequest;
      setPermissionResult(permission);
      if (permission !== "granted") return undefined;

      const registration = await navigator.serviceWorker.ready;
      const existing = await registration.pushManager.getSubscription();
      const subscription =
        existing ??
        (await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: decodeApplicationServerKey(
            configuration.data?.public_key ?? "",
          ),
        }));
      return enrollPushSubscription(subscription, session.csrf_token);
    },
    onSuccess: (response) => {
      if (!response) return;
      queryClient.setQueryData(PUSH_CONFIGURATION_QUERY_KEY, response);
      queryClient.setQueryData<ReconcileResponse>(
        PUSH_RECONCILIATION_QUERY_KEY,
        { enrolled: response.enrolled, remove_local: false },
      );
    },
  });
  const disable = useMutation({
    mutationFn: () => disablePushSubscription(session.csrf_token),
    onSuccess: () => {
      void unsubscribeLocalPushBestEffort();
      if (configuration.data) {
        queryClient.setQueryData(PUSH_CONFIGURATION_QUERY_KEY, {
          ...configuration.data,
          enrolled: false,
        });
      }
      queryClient.setQueryData<ReconcileResponse>(
        PUSH_RECONCILIATION_QUERY_KEY,
        { enrolled: false, remove_local: false },
      );
    },
  });

  const enrolled =
    reconciliation.data?.enrolled ?? configuration.data?.enrolled ?? false;
  const permission =
    permissionResult ?? (supported ? Notification.permission : "default");
  const error =
    configuration.error ??
    reconciliation.error ??
    enable.error ??
    disable.error;

  let guidance: string | undefined;
  if (isPublic) {
    guidance = "Push notifications are unavailable on public computers.";
  } else if (needsAppleInstallation) {
    guidance =
      "Add Memento to your iPhone or iPad Home Screen, then open it there to enable push notifications.";
  } else if (!supported) {
    guidance = "This browser does not support push notifications securely.";
  } else if (permission === "denied") {
    guidance =
      "Push permission is blocked. Allow notifications in your browser or device settings to enable them.";
  } else if (configuration.data && !configuration.data.available) {
    guidance = "Push notifications are not available from this Memento server.";
  }

  const busy = enable.isPending || disable.isPending;
  const canEnable =
    !guidance &&
    configuration.data?.available === true &&
    permission !== "denied" &&
    !enrolled;

  return (
    <section aria-labelledby="push-notifications-title" className="shell-card">
      <h2 id="push-notifications-title">Push notifications</h2>
      <p>
        Receive limited activity counts on this trusted device. Push is optional
        and independent of email and private Media access.
      </p>
      {configuration.isPending && !isPublic ? (
        <p aria-live="polite">Checking push availability…</p>
      ) : null}
      {guidance ? <p>{guidance}</p> : null}
      {error ? <p className="form-error">{error.message}</p> : null}
      {enrolled && !isPublic ? (
        <>
          <p aria-live="polite">
            Push notifications are enabled on this device.
          </p>
          <button
            className="danger-button"
            disabled={busy}
            onClick={() => disable.mutate()}
            type="button"
          >
            {disable.isPending ? "Disabling…" : "Disable push notifications"}
          </button>
        </>
      ) : null}
      {canEnable ? (
        <button
          disabled={busy}
          onClick={() => {
            const permissionRequest = Notification.requestPermission();
            enable.mutate(permissionRequest);
          }}
          type="button"
        >
          {enable.isPending ? "Enabling…" : "Enable push notifications"}
        </button>
      ) : null}
    </section>
  );
}
