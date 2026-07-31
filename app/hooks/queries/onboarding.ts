import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type {
  OnboardingCompleteResponse,
  OnboardingRequest,
  OnboardingResponse,
} from "../../types/generated/recipients";
import type { SessionResponse } from "../../types/generated/setup";
import { isIdentityGenerationActive } from "./sessions";

export function useOnboarding(identityGeneration: string) {
  return useQuery({
    queryKey: ["onboarding", identityGeneration],
    queryFn: () => apiJSON<OnboardingResponse>("/api/onboarding"),
    retry: false,
  });
}

export function useSaveOnboarding(
  identityGeneration: string,
  onSuccess: (response: OnboardingResponse) => void,
) {
  const queryClient = useQueryClient();
  const queryKey = ["onboarding", identityGeneration] as const;
  return useMutation({
    mutationFn: (request: OnboardingRequest) =>
      apiJSON<OnboardingResponse>("/api/onboarding", {
        method: "PATCH",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onMutate: async () => {
      await queryClient.cancelQueries({ queryKey, exact: true });
    },
    onSuccess: (response) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      queryClient.setQueryData(queryKey, response);
      onSuccess(response);
    },
  });
}

export function useCompleteOnboarding(
  session: SessionResponse,
  onSuccess: (session: SessionResponse) => void,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (request: OnboardingRequest) => {
      const response = await apiJSON<OnboardingCompleteResponse>(
        "/api/onboarding/complete",
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify(request),
        },
      );
      return {
        ...session,
        session_type: request.session_type,
        csrf_token: response.csrf_token,
        onboarding_required: false,
      };
    },
    onSuccess: (completedSession) => {
      queryClient.removeQueries({
        queryKey: ["onboarding", session.csrf_token],
      });
      onSuccess(completedSession);
    },
  });
}
