import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { APIError, apiJSON } from "../../api";
import { audienceKeys, eventKeys } from "./curationKeys";
import type {
  ApprovalResponse,
  AttendanceRequest,
  OverrideRequest,
  Review,
} from "../../types/generated/audiences";

export { audienceKeys } from "./curationKeys";

function invalidateAffectedProjections(
  queryClient: ReturnType<typeof useQueryClient>,
  identityGeneration: string,
) {
  void queryClient.invalidateQueries({
    queryKey: eventKeys.all(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: eventKeys.details(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: eventKeys.previewRecipientsRoot(identityGeneration),
  });
  void queryClient.invalidateQueries({
    queryKey: eventKeys.recipientPreviews(identityGeneration),
  });
}

export function useAudienceReview(
  identityGeneration: string,
  momentID: string,
  callbacks: {
    onAttendanceConfirmed: () => void;
    onAudienceChanged: () => void;
    onAudienceApproved: () => void;
  },
) {
  const queryClient = useQueryClient();
  const queryKey = audienceKeys.review(identityGeneration, momentID);
  const review = useQuery({
    queryKey,
    queryFn: () =>
      apiJSON<Review>(`/api/moments/${momentID}/attendance-audience`),
    retry: false,
  });

  const commitReview = (result: Review) => {
    queryClient.setQueryData<Review>(queryKey, (current) =>
      !current || result.version >= current.version ? result : current,
    );
    invalidateAffectedProjections(queryClient, identityGeneration);
  };

  const confirmAttendance = useMutation({
    mutationFn: (request: AttendanceRequest) =>
      apiJSON<Review>(`/api/moments/${momentID}/attendance`, {
        method: "PUT",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify(request),
      }),
    onSuccess: (result) => {
      commitReview(result);
      callbacks.onAttendanceConfirmed();
    },
  });
  const override = useMutation({
    mutationFn: (request: OverrideRequest) =>
      apiJSON<Review>(`/api/moments/${momentID}/audience/override`, {
        method: "PUT",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify(request),
      }),
    onSuccess: (result) => {
      commitReview(result);
      callbacks.onAudienceChanged();
    },
  });
  const recalculate = useMutation({
    mutationFn: () =>
      apiJSON<Review>(`/api/moments/${momentID}/audience/recalculate`, {
        method: "POST",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
      }),
    onSuccess: (result) => {
      commitReview(result);
      callbacks.onAudienceChanged();
    },
  });
  const approve = useMutation({
    mutationFn: () =>
      apiJSON<ApprovalResponse>(`/api/moments/${momentID}/audience/approve`, {
        method: "POST",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": identityGeneration,
        },
      }),
    onSuccess: (result) => {
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
      invalidateAffectedProjections(queryClient, identityGeneration);
      callbacks.onAudienceApproved();
    },
  });

  const errors = [
    review.error,
    confirmAttendance.error,
    override.error,
    recalculate.error,
    approve.error,
  ].filter((error): error is Error => error instanceof Error);

  return {
    review,
    confirmAttendance,
    override,
    recalculate,
    approve,
    errors,
    hasConflict: errors.some(
      (error) => error instanceof APIError && error.status === 409,
    ),
    reset: async () => {
      confirmAttendance.reset();
      override.reset();
      recalculate.reset();
      approve.reset();
      await review.refetch();
      invalidateAffectedProjections(queryClient, identityGeneration);
    },
  };
}
