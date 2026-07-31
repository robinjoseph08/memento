import { useMutation } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type { PlanRequest, PlanResponse } from "../../types/generated/archives";

export function usePrepareRecipientArchive(identityGeneration: string) {
  return useMutation({
    mutationFn: (request: PlanRequest) =>
      apiJSON<PlanResponse>("/api/me/archives", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
  });
}
