import { useMutation, useQuery } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type {
  AcceptResponse,
  InspectResponse,
  TokenRequest,
} from "../../types/generated/recipients";
import type {
  SignInRequest,
  SignInResponse,
  SignInStartResponse,
  SignInVerifyRequest,
} from "../../types/generated/sessions";
import type { SessionResponse } from "../../types/generated/setup";

export function useRequestSignInCode(
  onSuccess: (response: SignInStartResponse) => void,
) {
  return useMutation({
    mutationFn: (request: SignInRequest) =>
      apiJSON<SignInStartResponse>("/api/auth/sign-in/request", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess,
  });
}

export function useVerifySignIn(onSuccess: () => void) {
  return useMutation({
    mutationFn: (request: SignInVerifyRequest) =>
      apiJSON<SignInResponse>("/api/auth/sign-in/verify", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess,
  });
}

export function useConfirmSession(
  identityGeneration: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["sign-in-session", identityGeneration],
    queryFn: () => apiJSON<SessionResponse>("/api/session"),
    enabled,
    retry: false,
  });
}

export function useInvitation(token: string) {
  return useQuery({
    queryKey: ["invitation", token],
    queryFn: () =>
      apiJSON<InspectResponse>("/api/auth/invitations/inspect", {
        headers: { "X-Memento-Invitation": token },
      }),
    enabled: token.length > 0,
    retry: false,
  });
}

export function useAcceptInvitation(
  token: string,
  onSuccess: (response: AcceptResponse) => void,
) {
  return useMutation({
    mutationFn: () => {
      const request: TokenRequest = { token };
      return apiJSON<AcceptResponse>("/api/auth/invitations/accept", {
        method: "POST",
        body: JSON.stringify(request),
      });
    },
    onSuccess,
  });
}

export function useAcceptedInvitationSession(
  identityGeneration: number,
  online: boolean,
) {
  return useQuery({
    queryKey: ["accepted-invitation-session", identityGeneration],
    queryFn: () => apiJSON<SessionResponse>("/api/session"),
    enabled: identityGeneration > 0 && online,
    retry: 2,
    retryDelay: 0,
  });
}
