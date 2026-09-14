import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  Approval,
  ApproveRequest,
  Delivery,
  Notification,
  NotificationList,
  Preview,
  UnsubscribeStatus,
} from "../../types/generated/notifications";
import { useIdentityStatus } from "./identity";
import { usePrivateScope } from "./people";

// A member's own notifications back the bell and its list. Curators never
// receive viewer updates, so the query stays off for them. Returning to the
// window refreshes the unread count without a manual reload.
export function useNotifications() {
  const scope = usePrivateScope();
  const { data } = useIdentityStatus();
  const person = data?.person;
  return useQuery({
    queryKey: [...scope, "notifications"],
    queryFn: ({ signal }) =>
      request<NotificationList>("/api/notifications", { signal }),
    enabled: !!person && !person.is_curator && !!person.onboarding_completed_at,
    staleTime: 30_000,
    refetchOnWindowFocus: "always",
    retry: false,
  });
}

export function useMarkNotificationRead() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (id: string) =>
      request<Notification>(
        `/api/notifications/${encodeURIComponent(id)}/read`,
        { body: {} },
      ),
    onSuccess: (notification) => {
      client.setQueryData<NotificationList>(
        [...scope, "notifications"],
        (list) =>
          list && {
            unread: list.notifications.filter(
              (item) => !item.read_at && item.id !== notification.id,
            ).length,
            notifications: list.notifications.map((item) =>
              item.id === notification.id ? notification : item,
            ),
          },
      );
    },
  });
}

export function useMarkAllNotificationsRead() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: () =>
      request<NotificationList>("/api/notifications/read-all", { body: {} }),
    onSuccess: (list) => client.setQueryData([...scope, "notifications"], list),
  });
}

// The preview is computed on request and never refetched in the background:
// a Curator reviews one snapshot and either sends it or asks for a new one.
export function useUpdatePreview() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "notification-preview"],
    queryFn: ({ signal }) =>
      request<Preview>("/api/curator/notifications/preview", {
        body: {},
        signal,
      }),
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

// Approval does not invalidate the preview on purpose: the page keeps the
// reviewed snapshot and the outcome side by side, and asks for a new preview
// only when the Curator chooses "Check again". The dashboard is refreshed
// because sending changes what is unannounced and may queue email.
export function useApproveUpdates() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: ApproveRequest) =>
      request<Approval>("/api/curator/notifications/approve", { body }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "dashboard"] }),
  });
}

function settling(deliveries: Record<string, Delivery> | undefined) {
  return Object.values(deliveries ?? {}).some(
    (delivery) => delivery.status === "queued" || delivery.status === "sending",
  );
}

// Watches a batch of deliveries settle after sending, polling only while one
// is still queued or sending.
export function useDeliveries(ids: string[]) {
  const scope = usePrivateScope();
  const sorted = [...ids].sort();
  return useQuery({
    queryKey: [...scope, "deliveries", sorted],
    queryFn: ({ signal }) =>
      request<Record<string, Delivery>>(
        `/api/curator/notifications/deliveries?${sorted
          .map((id) => `id=${encodeURIComponent(id)}`)
          .join("&")}`,
        { signal },
      ),
    enabled: sorted.length > 0,
    retry: false,
    refetchInterval: (query) => (settling(query.state.data) ? 2000 : false),
  });
}

// A deliberate resend for a failed or uncertain update or alert email. The
// dashboard and any watched batch refresh so the new attempt is visible
// everywhere. Invitations retry through Identity instead.
export function useRetryDelivery() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (id: string) =>
      request<Delivery>(
        `/api/curator/notifications/deliveries/${encodeURIComponent(id)}/retry`,
        { body: {} },
      ),
    onSuccess: async (delivery) => {
      client.setQueriesData<Record<string, Delivery>>(
        { queryKey: [...scope, "deliveries"] },
        (current) =>
          current && current[delivery.id]
            ? { ...current, [delivery.id]: delivery }
            : current,
      );
      await client.invalidateQueries({ queryKey: [...scope, "dashboard"] });
    },
  });
}

// The unsubscribe link is public: loading it only describes the link's owner.
export function useUnsubscribeStatus(token: string) {
  return useQuery({
    queryKey: ["unsubscribe", token],
    queryFn: ({ signal }) =>
      request<UnsubscribeStatus>(
        `/api/unsubscribe?token=${encodeURIComponent(token)}`,
        { signal },
      ),
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
}

// Only this confirmation changes the preference.
export function useUnsubscribe(token: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () =>
      request<UnsubscribeStatus>(
        `/api/unsubscribe?token=${encodeURIComponent(token)}`,
        { body: {} },
      ),
    onSuccess: (status) => client.setQueryData(["unsubscribe", token], status),
  });
}
