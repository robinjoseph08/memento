import { useMutation, useQuery } from "@tanstack/react-query";

import { HTTPError } from "@/lib/http";
import { openSignIn, platformLabel } from "@/lib/sign-in";
import { useConnection, useCreateHTTP } from "@/providers";
import type {
  ExchangeMobileCodeRequest,
  MobileSession,
} from "@/types/generated/identity";
import type { ViewerAlbum } from "@/types/generated/publishing";

export const SIGN_IN_FAILED = "Sign-in could not be completed. Try again.";

// useSignIn runs the web sign-in in the browser sheet and exchanges the code
// it comes back with for this phone's own session. It resolves to null when
// the Person closed the sheet instead. Only what Memento itself said is
// shown; anything the sheet or the network threw becomes SIGN_IN_FAILED.
export function useSignIn(origin: string) {
  const createHTTP = useCreateHTTP();
  const { startSession } = useConnection();
  return useMutation({
    mutationFn: async () => {
      try {
        const code = await openSignIn(origin);
        if (code === null) {
          return null;
        }
        const request: ExchangeMobileCodeRequest = {
          code,
          platform: platformLabel(),
        };
        return await createHTTP(origin).request<MobileSession>(
          "/api/identity/mobile/exchange",
          { body: request },
        );
      } catch (error) {
        throw error instanceof HTTPError ? error : new Error(SIGN_IN_FAILED);
      }
    },
    onSuccess: async (session) => {
      if (session) {
        await startSession(session.token);
      }
    },
  });
}

// useSignOut ends the session on the server, then forgets it on the phone.
// The phone forgets either way: a session the server did not hear about
// still shows in the Person's sessions on the web, where it can be ended.
export function useSignOut(origin: string, token: string) {
  const createHTTP = useCreateHTTP();
  const { endSession } = useConnection();
  return useMutation({
    mutationFn: async () => {
      try {
        await createHTTP(origin, token).request<void>(
          "/api/identity/sign-out",
          { body: {} },
        );
      } catch {
        // The server may be unreachable or the session already gone.
      }
    },
    onSettled: () => endSession(),
  });
}

// useAlbums lists every Album the Person can see at the Installation.
export function useAlbums(origin: string, token: string) {
  const createHTTP = useCreateHTTP();
  return useQuery({
    queryKey: [origin, "albums"],
    queryFn: ({ signal }) =>
      createHTTP(origin, token).request<ViewerAlbum[]>("/api/albums", {
        signal,
      }),
  });
}
