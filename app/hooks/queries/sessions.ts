import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";

import { apiJSON, apiNoContent } from "../../api";
import type {
  EmailChangeCompleteRequest,
  EmailChangeRequest,
  EmailChangeResponse,
  EmailChangeStartResponse,
  ListResponse,
  RenameRequest,
} from "../../types/generated/sessions";
import type { SessionResponse } from "../../types/generated/setup";

export const CURRENT_SESSION_QUERY_KEY = ["current-session"] as const;

export function useCurrentSession() {
  return useQuery({
    queryKey: CURRENT_SESSION_QUERY_KEY,
    queryFn: () => apiJSON<SessionResponse>("/api/session"),
    enabled: false,
    retry: false,
  });
}

export function isIdentityGenerationActive(
  queryClient: QueryClient,
  identityGeneration: string,
) {
  return (
    queryClient.getQueryData<SessionResponse>(CURRENT_SESSION_QUERY_KEY)
      ?.csrf_token === identityGeneration
  );
}

export function useSessions(identityGeneration: string) {
  return useQuery({
    queryKey: ["sessions", identityGeneration],
    queryFn: () => apiJSON<ListResponse>("/api/sessions"),
    retry: false,
  });
}

export function useRenameSession(
  identityGeneration: string,
  onSuccess: (id: string) => void,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, request }: { id: string; request: RenameRequest }) =>
      apiNoContent(`/api/sessions/${id}`, {
        method: "PATCH",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onSuccess: async (_, { id }) => {
      await queryClient.invalidateQueries({
        queryKey: ["sessions", identityGeneration],
      });
      onSuccess(id);
    },
  });
}

export function useRevokeSession(
  identityGeneration: string,
  isCurrent: (id: string) => boolean,
  onSignedOut: () => void,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiNoContent(`/api/sessions/${id}`, {
        method: "DELETE",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess: async (_, id) => {
      if (isCurrent(id)) onSignedOut();
      else {
        await queryClient.invalidateQueries({
          queryKey: ["sessions", identityGeneration],
        });
      }
    },
  });
}

export function useSignOutAllSessions(
  identityGeneration: string,
  onSignedOut: () => void,
) {
  return useMutation({
    mutationFn: () =>
      apiNoContent("/api/sessions/sign-out-all", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess: onSignedOut,
  });
}

export function useStartEmailChange(
  identityGeneration: string,
  onSuccess: (response: EmailChangeStartResponse) => void,
) {
  return useMutation({
    mutationFn: (request: EmailChangeRequest) =>
      apiJSON<EmailChangeStartResponse>("/api/me/email-change/request", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onSuccess,
  });
}

export function useCompleteEmailChange(identityGeneration: string) {
  return useMutation({
    mutationFn: (request: EmailChangeCompleteRequest) =>
      apiJSON<EmailChangeResponse>("/api/me/email-change/complete", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify(request),
      }),
    onSuccess: () => window.location.reload(),
  });
}

export function useSignOut(
  identityGeneration: string | undefined,
  onSuccess: () => void,
) {
  return useMutation({
    mutationFn: () => {
      if (!identityGeneration)
        throw new Error("A Session is required to sign out.");
      return apiNoContent("/api/session/logout", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
      });
    },
    onSuccess,
  });
}
