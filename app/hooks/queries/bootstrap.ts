import { useQuery } from "@tanstack/react-query";

import { APIError, apiJSON, apiNoContent } from "../../api";
import type {
  AvailabilityResponse,
  SessionResponse,
} from "../../types/generated/setup";
import type { StatusResponse as RecoveryStatusResponse } from "../../types/generated/recovery";

export type BootstrapState =
  | { kind: "available" }
  | { kind: "session"; session: SessionResponse }
  | { kind: "recovery"; session?: SessionResponse }
  | { kind: "closed" };

async function fetchBootstrap(): Promise<BootstrapState> {
  try {
    await apiJSON<AvailabilityResponse>("/api/setup");
    return { kind: "available" };
  } catch (error) {
    if (error instanceof APIError && error.status === 503) {
      const recovery = await apiJSON<RecoveryStatusResponse>(
        "/api/recovery/status",
      );
      if (recovery.held) {
        try {
          const session = await apiJSON<SessionResponse>("/api/session");
          return { kind: "recovery", session };
        } catch (sessionError) {
          if (sessionError instanceof APIError && sessionError.status === 401) {
            return { kind: "recovery" };
          }
          throw sessionError;
        }
      }
    }
    if (!(error instanceof APIError) || error.status !== 404) throw error;
  }

  try {
    const session = await apiJSON<SessionResponse>("/api/session");
    if (session.session_type === "trusted") {
      await apiNoContent("/api/session/refresh", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      });
    }
    return { kind: "session", session };
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      return { kind: "closed" };
    }
    throw error;
  }
}

export function useBootstrap(enabled: boolean) {
  return useQuery({
    queryKey: ["bootstrap"],
    queryFn: fetchBootstrap,
    enabled,
    retry: false,
  });
}
