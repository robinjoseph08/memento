import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import {
  audienceKeys,
  eventKeys,
  looseItemKeys,
  sourceKeys,
} from "./curationKeys";
import { isIdentityGenerationActive } from "./sessions";
import type {
  CreateEventRequest,
  CreateLooseItemRequest,
  Event,
  EventListResponse,
  LooseItem,
  OrganizeEventRequest,
  PreviewRecipientsResponse,
  PublicationResponse,
  PublishEventRequest,
  PublishedEventView,
  RestorePublishedMediaRequest,
  Withdrawal,
  WithdrawalTarget,
  WithdrawRequest,
} from "../../types/generated/events";

export { eventKeys } from "./curationKeys";

export type EventMutationAttempt = {
  event: Event;
  revision: number;
  selectionGeneration: number;
};
export type RestoreEventAttempt = EventMutationAttempt & { mediaID: string };
export type PublishEventAttempt = EventMutationAttempt & {
  notifyRecipients: boolean;
};
export type WithdrawEventAttempt = EventMutationAttempt & {
  target: WithdrawalTarget;
  reason: string;
};

function organizationRequest(event: Event): OrganizeEventRequest {
  return {
    version: event.version,
    title: event.title,
    description: event.description,
    date_start: event.date_start,
    date_end: event.date_end,
    selected_cover_media_item_id: event.selected_cover_media_item_id,
    place_labels: event.place_labels,
    grouping_timezone: event.grouping_timezone,
    moments: event.moments.map((moment) => ({
      id: moment.id,
      title: moment.title,
      place_labels: moment.place_labels,
      proposed_day: moment.proposed_day,
      cover_media_item_id: moment.cover_media_item_id,
      media_item_ids: moment.media_items.map((item) => item.id),
    })),
    unassigned_media_ids: event.unassigned_media.map((item) => item.id),
    final_review_complete: event.final_review_complete,
  };
}

function setNewestEvent(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
  event: Event,
) {
  queryClient.setQueryData<Event>(
    eventKeys.detail(identityGeneration, event.id),
    (current) =>
      !current || event.version >= current.version ? event : current,
  );
}

function publishedRecipientProjectionKeys(identityGeneration: string) {
  return [
    ["recipient-library", identityGeneration],
    ["recipient-events", identityGeneration],
    ["recipient-event", identityGeneration],
    ["new-for-you", identityGeneration],
    ["recipient-search", identityGeneration],
  ];
}

function invalidatePublishedRecipientProjections(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
) {
  for (const queryKey of publishedRecipientProjectionKeys(identityGeneration))
    void queryClient.invalidateQueries({ queryKey });
}

function removePublishedRecipientProjections(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
) {
  for (const queryKey of publishedRecipientProjectionKeys(identityGeneration))
    queryClient.removeQueries({ queryKey });
}

function invalidateEventProjections(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
  eventID: string,
  invalidateDetail = false,
) {
  void queryClient.invalidateQueries({
    queryKey: eventKeys.all(identityGeneration),
  });
  if (invalidateDetail)
    void queryClient.invalidateQueries({
      queryKey: eventKeys.detail(identityGeneration, eventID),
      exact: true,
    });
  void queryClient.invalidateQueries({
    queryKey: audienceKeys.all(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: eventKeys.recipientPreviews(identityGeneration),
  });
}

export function useCreateEventDraft(identityGeneration: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: CreateEventRequest) =>
      apiJSON<Event>("/api/events", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onSuccess: (event) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      setNewestEvent(queryClient, identityGeneration, event);
      void queryClient.invalidateQueries({
        queryKey: eventKeys.all(identityGeneration),
      });
      void queryClient.invalidateQueries({
        queryKey: sourceKeys.all(identityGeneration),
      });
    },
    onError: () => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      void queryClient.invalidateQueries({
        queryKey: sourceKeys.all(identityGeneration),
      });
      void queryClient.invalidateQueries({
        queryKey: sourceKeys.mediaRoot(identityGeneration),
      });
    },
  });
}

export function useCreateLooseItem(identityGeneration: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: CreateLooseItemRequest) =>
      apiJSON<LooseItem>("/api/loose-items", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onSuccess: (looseItem) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      queryClient.setQueryData(
        looseItemKeys.detail(identityGeneration, looseItem.id),
        looseItem,
      );
      void queryClient.invalidateQueries({
        queryKey: looseItemKeys.all(identityGeneration),
      });
      void queryClient.invalidateQueries({
        queryKey: sourceKeys.mediaRoot(identityGeneration),
      });
    },
    onError: () => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      void queryClient.invalidateQueries({
        queryKey: looseItemKeys.all(identityGeneration),
      });
      void queryClient.invalidateQueries({
        queryKey: sourceKeys.mediaRoot(identityGeneration),
      });
    },
  });
}

export function useEvents(identityGeneration: string) {
  return useQuery({
    queryKey: eventKeys.all(identityGeneration),
    queryFn: () => apiJSON<EventListResponse>("/api/events"),
    retry: false,
  });
}

export function useEvent(identityGeneration: string, eventID: string) {
  const query = useQuery({
    queryKey: eventKeys.detail(identityGeneration, eventID),
    queryFn: () => apiJSON<Event>(`/api/events/${eventID}`),
    enabled: eventID.length > 0,
    retry: false,
  });
  return { ...query, refetchAuthoritative: query.refetch };
}

export function usePreviewRecipients(
  identityGeneration: string,
  eventID: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: eventKeys.previewRecipients(identityGeneration, eventID),
    queryFn: () =>
      apiJSON<PreviewRecipientsResponse>(
        `/api/events/${eventID}/preview-recipients`,
      ),
    enabled: eventID.length > 0 && enabled,
    retry: false,
  });
}

export function useRecipientPreview(
  identityGeneration: string,
  eventID: string,
  eventVersion: number | undefined,
  recipientID: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: eventKeys.recipientPreview(
      identityGeneration,
      eventID,
      eventVersion,
      recipientID,
    ),
    queryFn: () =>
      apiJSON<PublishedEventView>(
        `/api/events/${eventID}/preview?recipient_person_id=${encodeURIComponent(recipientID)}`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": identityGeneration },
        },
      ),
    enabled: enabled && eventID.length > 0 && recipientID.length > 0,
    retry: false,
  });
}

export function useOrganizeEvent(
  identityGeneration: string,
  callbacks: {
    onMutate: () => void;
    onSuccess: (event: Event, attempt: EventMutationAttempt) => void;
    onError: (error: Error, attempt: EventMutationAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ event }: EventMutationAttempt) =>
      apiJSON<Event>(`/api/events/${event.id}/organization`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(organizationRequest(event)),
      }),
    onMutate: callbacks.onMutate,
    onSuccess: (event, attempt) => {
      setNewestEvent(queryClient, identityGeneration, event);
      invalidateEventProjections(queryClient, identityGeneration, event.id);
      callbacks.onSuccess(event, attempt);
    },
    onError: callbacks.onError,
  });
}

export function useRestorePublishedMedia(
  identityGeneration: string,
  callbacks: {
    onMutate?: () => void;
    onSuccess: (event: Event, attempt: RestoreEventAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ event, mediaID }: RestoreEventAttempt) =>
      apiJSON<Event>(`/api/events/${event.id}/published-media-restorations`, {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify({
          version: event.version,
          media_item_id: mediaID,
        } satisfies RestorePublishedMediaRequest),
      }),
    onMutate: callbacks.onMutate,
    onSuccess: (event, attempt) => {
      setNewestEvent(queryClient, identityGeneration, event);
      invalidateEventProjections(queryClient, identityGeneration, event.id);
      callbacks.onSuccess(event, attempt);
    },
  });
}

export function usePublishEvent(
  identityGeneration: string,
  callbacks: {
    onStarted: (attempt: PublishEventAttempt) => void;
    onCommitted: (
      publication: PublicationResponse,
      attempt: PublishEventAttempt,
    ) => void;
    onSuccess: (
      publication: PublicationResponse,
      attempt: PublishEventAttempt,
      authoritativeEvent: Event | undefined,
    ) => void;
    onError: (error: Error, attempt: PublishEventAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ event, notifyRecipients }: PublishEventAttempt) =>
      apiJSON<PublicationResponse>(`/api/events/${event.id}/publications`, {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify({
          version: event.version,
          notify_recipients: notifyRecipients,
        } satisfies PublishEventRequest),
      }),
    onMutate: (attempt) => {
      queryClient.removeQueries({
        queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
      });
      queryClient.removeQueries({
        queryKey: eventKeys.recipientPreviews(identityGeneration),
      });
      callbacks.onStarted(attempt);
    },
    onError: (error, attempt) => {
      void queryClient.invalidateQueries({
        queryKey: eventKeys.details(identityGeneration),
      });
      invalidatePublishedRecipientProjections(queryClient, identityGeneration);
      callbacks.onError(error, attempt);
    },
    onSuccess: async (publication, attempt) => {
      queryClient.removeQueries({
        queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
      });
      queryClient.removeQueries({
        queryKey: eventKeys.recipientPreviews(identityGeneration),
      });
      callbacks.onCommitted(publication, attempt);
      let authoritativeEvent: Event | undefined;
      try {
        authoritativeEvent = await apiJSON<Event>(
          `/api/events/${attempt.event.id}`,
        );
        setNewestEvent(queryClient, identityGeneration, authoritativeEvent);
      } catch {
        // The authoritative detail stays unchanged until its invalidation loads.
      }
      void queryClient.invalidateQueries({
        queryKey: eventKeys.details(identityGeneration),
        predicate: (query) => query.queryKey[2] !== attempt.event.id,
      });
      invalidateEventProjections(
        queryClient,
        identityGeneration,
        attempt.event.id,
        true,
      );
      invalidatePublishedRecipientProjections(queryClient, identityGeneration);
      callbacks.onSuccess(publication, attempt, authoritativeEvent);
    },
  });
}

export function useWithdrawEvent(
  identityGeneration: string,
  callbacks: {
    onStarted: (attempt: WithdrawEventAttempt) => void;
    onCommitted: (
      withdrawal: Withdrawal,
      attempt: WithdrawEventAttempt,
    ) => void;
    onSuccess: (
      withdrawal: Withdrawal,
      attempt: WithdrawEventAttempt,
      authoritativeEvent: Event | undefined,
    ) => void;
    onError: (error: Error, attempt: WithdrawEventAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ target, reason }: WithdrawEventAttempt) =>
      apiJSON<Withdrawal>("/api/withdrawals", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify({
          target_kind: target.target_kind,
          target_id: target.target_id,
          reason,
        } satisfies WithdrawRequest),
      }),
    onMutate: (attempt) => {
      queryClient.removeQueries({
        queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
      });
      queryClient.removeQueries({
        queryKey: eventKeys.recipientPreviews(identityGeneration),
      });
      removePublishedRecipientProjections(queryClient, identityGeneration);
      callbacks.onStarted(attempt);
    },
    onError: (error, attempt) => {
      void queryClient.invalidateQueries({
        queryKey: eventKeys.details(identityGeneration),
      });
      invalidatePublishedRecipientProjections(queryClient, identityGeneration);
      callbacks.onError(error, attempt);
    },
    onSuccess: async (withdrawal, attempt) => {
      queryClient.removeQueries({
        queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
      });
      queryClient.removeQueries({
        queryKey: eventKeys.recipientPreviews(identityGeneration),
      });
      callbacks.onCommitted(withdrawal, attempt);
      let authoritativeEvent: Event | undefined;
      try {
        authoritativeEvent = await apiJSON<Event>(
          `/api/events/${attempt.event.id}`,
        );
        setNewestEvent(queryClient, identityGeneration, authoritativeEvent);
      } catch {
        void queryClient.invalidateQueries({
          queryKey: eventKeys.detail(identityGeneration, attempt.event.id),
          exact: true,
        });
      }
      void queryClient.invalidateQueries({
        queryKey: eventKeys.details(identityGeneration),
        predicate: (query) => query.queryKey[2] !== attempt.event.id,
      });
      invalidateEventProjections(
        queryClient,
        identityGeneration,
        attempt.event.id,
        true,
      );
      invalidatePublishedRecipientProjections(queryClient, identityGeneration);
      callbacks.onSuccess(withdrawal, attempt, authoritativeEvent);
    },
  });
}
