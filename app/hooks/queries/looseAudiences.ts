import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { APIError, apiJSON } from "../../api";
import { looseItemKeys } from "./curationKeys";
import { isIdentityGenerationActive } from "./sessions";
import type {
  ApprovalResponse,
  OverrideRequest,
  Review,
} from "../../types/generated/audiences";

function invalidate(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
) {
  for (const queryKey of [
    looseItemKeys.all(identityGeneration),
    looseItemKeys.details(identityGeneration),
    looseItemKeys.previewRecipientsRoot(identityGeneration),
    looseItemKeys.recipientPreviews(identityGeneration),
  ])
    void queryClient.invalidateQueries({ queryKey });
  void queryClient.invalidateQueries({ queryKey: ["new-for-you"] });
  void queryClient.invalidateQueries({ queryKey: ["recipient-library"] });
  void queryClient.invalidateQueries({ queryKey: ["recipient-loose-item"] });
}

export function useLooseAudienceReview(
  identityGeneration: string,
  looseItemID: string,
  callbacks: {
    onAudienceChanged: () => void;
    onAudienceApproved: () => void;
  },
) {
  const queryClient = useQueryClient();
  const queryKey = looseItemKeys.audience(identityGeneration, looseItemID);
  const review = useQuery({
    queryKey,
    queryFn: () =>
      apiJSON<Review>(`/api/loose-items/${looseItemID}/attendance-audience`),
    enabled: looseItemID.length > 0,
    retry: false,
  });
  const commit = (result: Review) => {
    queryClient.setQueryData<Review>(queryKey, (current) =>
      !current || result.version >= current.version ? result : current,
    );
    invalidate(queryClient, identityGeneration);
  };
  const override = useMutation({
    mutationFn: (request: OverrideRequest) =>
      apiJSON<Review>(`/api/loose-items/${looseItemID}/audience/override`, {
        method: "PUT",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify(request),
      }),
    onSuccess: (result) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      commit(result);
      callbacks.onAudienceChanged();
    },
  });
  const recalculate = useMutation({
    mutationFn: () =>
      apiJSON<Review>(`/api/loose-items/${looseItemID}/audience/recalculate`, {
        method: "POST",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
      }),
    onSuccess: (result) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      commit(result);
      callbacks.onAudienceChanged();
    },
  });
  const approve = useMutation({
    mutationFn: () =>
      apiJSON<ApprovalResponse>(
        `/api/loose-items/${looseItemID}/audience/approve`,
        {
          method: "POST",
          headers: {
            "If-Match": String(review.data?.version ?? 0),
            "X-Memento-CSRF": identityGeneration,
          },
        },
      ),
    onSuccess: (result) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      queryClient.setQueryData<Review>(queryKey, (current) =>
        current && result.version >= current.version
          ? {
              ...current,
              approved_audience: result.audience,
              audience_complete: true,
              version: result.version,
            }
          : current,
      );
      invalidate(queryClient, identityGeneration);
      callbacks.onAudienceApproved();
    },
  });
  const errors = [
    review.error,
    override.error,
    recalculate.error,
    approve.error,
  ].filter((error): error is Error => error instanceof Error);
  return {
    review,
    override,
    recalculate,
    approve,
    errors,
    hasConflict: errors.some(
      (error) => error instanceof APIError && error.status === 409,
    ),
    reset: async () => {
      override.reset();
      recalculate.reset();
      approve.reset();
      await review.refetch();
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      invalidate(queryClient, identityGeneration);
    },
  };
}
