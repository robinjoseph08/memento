import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef } from "react";

import { apiJSON, apiNoContent } from "../../api";
import type { ReviewResponse } from "../../types/generated/recovery";
import type { SessionResponse } from "../../types/generated/setup";

export function useRecoveryReview(session?: SessionResponse) {
  return useQuery({
    queryKey: ["recovery-review", session?.csrf_token],
    queryFn: () => apiJSON<ReviewResponse>("/api/recovery/review"),
    enabled: Boolean(session?.curator),
    retry: false,
  });
}

export function useReleaseRecovery(
  session: SessionResponse | undefined,
  onReleaseAttempted: () => void,
) {
  const reviewSession = useRef<string | undefined>(undefined);
  const reviewAcknowledged = useRef(false);
  const releaseAttempted = useRef(false);

  return useMutation({
    mutationFn: async () => {
      releaseAttempted.current = false;
      if (!session) throw new Error("A fresh Curator Session is required.");
      if (reviewSession.current !== session.csrf_token) {
        reviewSession.current = session.csrf_token;
        reviewAcknowledged.current = false;
      }
      const headers = { "X-Memento-CSRF": session.csrf_token };
      if (!reviewAcknowledged.current) {
        await apiNoContent("/api/recovery/review/complete", {
          method: "POST",
          headers,
        });
        reviewAcknowledged.current = true;
      }
      releaseAttempted.current = true;
      await apiNoContent("/api/recovery/release", {
        method: "POST",
        headers,
      });
    },
    onSettled: () => {
      if (releaseAttempted.current) onReleaseAttempted();
    },
  });
}
