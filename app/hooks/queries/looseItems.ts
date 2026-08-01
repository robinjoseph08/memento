import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import { eventKeys, looseItemKeys } from "./curationKeys";
import { isIdentityGenerationActive } from "./sessions";
import type {
  LooseItem,
  LooseItemListResponse,
  LoosePublicationResponse,
  PreviewRecipientsResponse,
  PublishedLooseItemView,
  PublishLooseItemRequest,
  UpdateLooseItemRequest,
  Withdrawal,
  WithdrawRequest,
} from "../../types/generated/events";

export { looseItemKeys } from "./curationKeys";

export type LooseItemAttempt = {
  looseItem: LooseItem;
  revision: number;
  selectionGeneration: number;
};
export type PublishLooseItemAttempt = LooseItemAttempt & {
  notifyRecipients: boolean;
};
export type WithdrawLooseItemAttempt = LooseItemAttempt & { reason: string };

function updateRequest(looseItem: LooseItem): UpdateLooseItemRequest {
  return {
    version: looseItem.version,
    title: looseItem.title,
    description: looseItem.description,
    grouping_timezone: looseItem.grouping_timezone,
    proposed_day: looseItem.proposed_day,
    place_labels: looseItem.place_labels,
  };
}

function setNewestLooseItem(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
  looseItem: LooseItem,
) {
  queryClient.setQueryData<LooseItem>(
    looseItemKeys.detail(identityGeneration, looseItem.id),
    (current) =>
      !current || looseItem.version >= current.version ? looseItem : current,
  );
}

function invalidateLooseItemProjections(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
  looseItemID: string,
  invalidateDetail = false,
  accessChanging = false,
) {
  void queryClient.invalidateQueries({
    queryKey: looseItemKeys.all(identityGeneration),
  });
  if (invalidateDetail)
    void queryClient.invalidateQueries({
      queryKey: looseItemKeys.detail(identityGeneration, looseItemID),
      exact: true,
    });
  void queryClient.invalidateQueries({
    queryKey: looseItemKeys.audiences(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: looseItemKeys.previewRecipientsRoot(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: looseItemKeys.recipientPreviews(identityGeneration),
  });
  void queryClient.invalidateQueries({ queryKey: ["new-for-you"] });
  void queryClient.invalidateQueries({ queryKey: ["recipient-library"] });
  void queryClient.invalidateQueries({ queryKey: ["recipient-loose-item"] });
  if (accessChanging) {
    void queryClient.invalidateQueries({
      queryKey: eventKeys.all(identityGeneration),
    });
    void queryClient.invalidateQueries({
      queryKey: eventKeys.details(identityGeneration),
    });
    void queryClient.invalidateQueries({
      queryKey: ["recipient-search", identityGeneration],
    });
  }
}

function evictLoosePreviews(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
) {
  queryClient.removeQueries({
    queryKey: looseItemKeys.previewRecipientsRoot(identityGeneration),
  });
  queryClient.removeQueries({
    queryKey: looseItemKeys.recipientPreviews(identityGeneration),
  });
}

export function useLooseItems(identityGeneration: string) {
  return useQuery({
    queryKey: looseItemKeys.all(identityGeneration),
    queryFn: () => apiJSON<LooseItemListResponse>("/api/loose-items"),
    retry: false,
  });
}

export function useLooseItem(identityGeneration: string, looseItemID: string) {
  const query = useQuery({
    queryKey: looseItemKeys.detail(identityGeneration, looseItemID),
    queryFn: () => apiJSON<LooseItem>(`/api/loose-items/${looseItemID}`),
    enabled: looseItemID.length > 0,
    retry: false,
  });
  return { ...query, refetchAuthoritative: query.refetch };
}

export function useUpdateLooseItem(
  identityGeneration: string,
  callbacks: {
    onMutate: (attempt: LooseItemAttempt) => void;
    onSuccess: (looseItem: LooseItem, attempt: LooseItemAttempt) => void;
    onError: (error: Error, attempt: LooseItemAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ looseItem }: LooseItemAttempt) =>
      apiJSON<LooseItem>(`/api/loose-items/${looseItem.id}`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(updateRequest(looseItem)),
      }),
    onMutate: (attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      callbacks.onMutate(attempt);
    },
    onSuccess: (looseItem, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      setNewestLooseItem(queryClient, identityGeneration, looseItem);
      invalidateLooseItemProjections(
        queryClient,
        identityGeneration,
        looseItem.id,
      );
      callbacks.onSuccess(looseItem, attempt);
    },
    onError: (error, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      callbacks.onError(error, attempt);
    },
  });
}

export function useLoosePreviewRecipients(
  identityGeneration: string,
  looseItemID: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: looseItemKeys.previewRecipients(identityGeneration, looseItemID),
    queryFn: () =>
      apiJSON<PreviewRecipientsResponse>(
        `/api/loose-items/${looseItemID}/preview-recipients`,
      ),
    enabled: enabled && looseItemID.length > 0,
    retry: false,
  });
}

export function useLooseRecipientPreview(
  identityGeneration: string,
  looseItemID: string,
  version: number | undefined,
  recipientID: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: looseItemKeys.recipientPreview(
      identityGeneration,
      looseItemID,
      version,
      recipientID,
    ),
    queryFn: () =>
      apiJSON<PublishedLooseItemView>(
        `/api/loose-items/${looseItemID}/preview?recipient_person_id=${encodeURIComponent(recipientID)}`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": identityGeneration },
        },
      ),
    enabled: enabled && looseItemID.length > 0 && recipientID.length > 0,
    retry: false,
  });
}

export function usePublishLooseItem(
  identityGeneration: string,
  callbacks: {
    onStarted: (attempt: PublishLooseItemAttempt) => void;
    onCommitted: (
      publication: LoosePublicationResponse,
      attempt: PublishLooseItemAttempt,
    ) => void;
    onSuccess: (
      publication: LoosePublicationResponse,
      attempt: PublishLooseItemAttempt,
      authoritativeLooseItem: LooseItem | undefined,
    ) => void;
    onError: (error: Error, attempt: PublishLooseItemAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ looseItem, notifyRecipients }: PublishLooseItemAttempt) =>
      apiJSON<LoosePublicationResponse>(
        `/api/loose-items/${looseItem.id}/publications`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": identityGeneration },
          body: JSON.stringify({
            version: looseItem.version,
            notify_recipients: notifyRecipients,
          } satisfies PublishLooseItemRequest),
        },
      ),
    onMutate: (attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      evictLoosePreviews(queryClient, identityGeneration);
      callbacks.onStarted(attempt);
    },
    onError: (error, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      invalidateLooseItemProjections(
        queryClient,
        identityGeneration,
        attempt.looseItem.id,
        true,
        true,
      );
      callbacks.onError(error, attempt);
    },
    onSuccess: async (publication, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      evictLoosePreviews(queryClient, identityGeneration);
      invalidateLooseItemProjections(
        queryClient,
        identityGeneration,
        attempt.looseItem.id,
        true,
        true,
      );
      callbacks.onCommitted(publication, attempt);
      let authoritativeLooseItem: LooseItem | undefined;
      try {
        authoritativeLooseItem = await apiJSON<LooseItem>(
          `/api/loose-items/${attempt.looseItem.id}`,
        );
      } catch {
        // Controls remain blocked until the organizer recovers authority.
      }
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      if (authoritativeLooseItem)
        setNewestLooseItem(
          queryClient,
          identityGeneration,
          authoritativeLooseItem,
        );
      callbacks.onSuccess(publication, attempt, authoritativeLooseItem);
    },
  });
}

export function useWithdrawLooseItem(
  identityGeneration: string,
  callbacks: {
    onStarted: (attempt: WithdrawLooseItemAttempt) => void;
    onCommitted: (
      withdrawal: Withdrawal,
      attempt: WithdrawLooseItemAttempt,
    ) => void;
    onSuccess: (
      withdrawal: Withdrawal,
      attempt: WithdrawLooseItemAttempt,
      authoritativeLooseItem: LooseItem | undefined,
    ) => void;
    onError: (error: Error, attempt: WithdrawLooseItemAttempt) => void;
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ looseItem, reason }: WithdrawLooseItemAttempt) =>
      apiJSON<Withdrawal>("/api/withdrawals", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify({
          target_kind: "loose_item",
          target_id: looseItem.id,
          reason,
        } satisfies WithdrawRequest),
      }),
    onMutate: (attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      evictLoosePreviews(queryClient, identityGeneration);
      callbacks.onStarted(attempt);
    },
    onError: (error, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      invalidateLooseItemProjections(
        queryClient,
        identityGeneration,
        attempt.looseItem.id,
        true,
        true,
      );
      callbacks.onError(error, attempt);
    },
    onSuccess: async (withdrawal, attempt) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      evictLoosePreviews(queryClient, identityGeneration);
      invalidateLooseItemProjections(
        queryClient,
        identityGeneration,
        attempt.looseItem.id,
        true,
        true,
      );
      callbacks.onCommitted(withdrawal, attempt);
      let authoritativeLooseItem: LooseItem | undefined;
      try {
        authoritativeLooseItem = await apiJSON<LooseItem>(
          `/api/loose-items/${attempt.looseItem.id}`,
        );
      } catch {
        // Controls remain blocked until the organizer recovers authority.
      }
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      if (authoritativeLooseItem)
        setNewestLooseItem(
          queryClient,
          identityGeneration,
          authoritativeLooseItem,
        );
      callbacks.onSuccess(withdrawal, attempt, authoritativeLooseItem);
    },
  });
}
