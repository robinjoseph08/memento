import { useMutation } from "@tanstack/react-query";

import { APIError, apiJSON } from "../../api";
import type {
  CompleteRequest,
  CompleteResponse,
  RequestCodeRequest,
  RequestCodeResponse,
  SessionResponse,
  VerifyCodeRequest,
  VerifyCodeResponse,
} from "../../types/generated/setup";

export function useRequestSetupCode(
  onSuccess: (response: RequestCodeResponse) => void,
) {
  return useMutation({
    mutationFn: (request: RequestCodeRequest) =>
      apiJSON<RequestCodeResponse>("/api/setup/code", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess,
  });
}

export function useVerifySetupCode(
  onSuccess: (response: VerifyCodeResponse) => void,
) {
  return useMutation({
    mutationFn: (request: VerifyCodeRequest) =>
      apiJSON<VerifyCodeResponse>("/api/setup/verify", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess,
  });
}

export function useCompleteSetup(
  onSuccess: (session: SessionResponse) => void,
) {
  return useMutation({
    mutationFn: async (request: CompleteRequest) => {
      const completed = await apiJSON<CompleteResponse>("/api/setup/complete", {
        method: "POST",
        body: JSON.stringify(request),
      });
      const session = await apiJSON<SessionResponse>("/api/session");
      if (session.csrf_token !== completed.csrf_token) {
        throw new APIError("The new Session could not be confirmed.", 500);
      }
      return session;
    },
    onSuccess,
  });
}
