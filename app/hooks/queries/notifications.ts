import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import { isIdentityGenerationActive } from "./sessions";
import type {
  EmailPreferenceRequest,
  EmailPreferenceResponse,
  PlatformEmailDefaultsRequest,
  PlatformEmailDefaultsResponse,
} from "../../types/generated/recipients";

export function useEmailPreferences(
  identityGeneration: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["email-preferences", identityGeneration],
    queryFn: () =>
      apiJSON<EmailPreferenceResponse>("/api/me/email-preferences"),
    enabled,
    retry: false,
  });
}

export function useUpdateEmailPreferences(
  identityGeneration: string,
  onSuccess: (response: EmailPreferenceResponse) => void,
) {
  const queryClient = useQueryClient();
  const queryKey = ["email-preferences", identityGeneration] as const;
  return useMutation({
    mutationFn: (request: EmailPreferenceRequest) =>
      apiJSON<EmailPreferenceResponse>("/api/me/email-preferences", {
        method: "PUT",
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

export function usePlatformEmailDefaults(
  identityGeneration: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["platform-email-defaults", identityGeneration],
    queryFn: () =>
      apiJSON<PlatformEmailDefaultsResponse>("/api/curator/email-defaults"),
    enabled,
    retry: false,
  });
}

export function useUpdatePlatformEmailDefaults(
  identityGeneration: string,
  onSuccess: (response: PlatformEmailDefaultsResponse) => void,
) {
  const queryClient = useQueryClient();
  const queryKey = ["platform-email-defaults", identityGeneration] as const;
  return useMutation({
    mutationFn: (request: PlatformEmailDefaultsRequest) =>
      apiJSON<PlatformEmailDefaultsResponse>("/api/curator/email-defaults", {
        method: "PUT",
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
