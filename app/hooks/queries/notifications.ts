import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  Approval,
  ApproveRequest,
  Notification,
  NotificationList,
  Preview,
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

export function useApproveUpdates() {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: ApproveRequest) =>
      request<Approval>("/api/curator/notifications/approve", { body }),
  });
}
