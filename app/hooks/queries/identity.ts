import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import { clearPrivateQueries, statusKey } from "../../lib/query-client";
import type {
  Person,
  SignInRequest,
  Status,
} from "../../types/generated/identity";

export function useIdentityStatus() {
  const client = useQueryClient();
  return useQuery({
    queryKey: statusKey,
    queryFn: async ({ signal }) => {
      const status = await request<Status>("/api/identity/status", { signal });
      signal.throwIfAborted();
      const previous = client.getQueryData<Status>(statusKey);
      if (
        previous?.person?.id !== status.person?.id ||
        previous?.person?.is_curator !== status.person?.is_curator
      )
        clearPrivateQueries(client);
      return status;
    },
    retry: false,
    staleTime: 30_000,
    refetchOnWindowFocus: "always",
  });
}

export function useSignOut(everywhere = false) {
  const client = useQueryClient();
  const { data } = useIdentityStatus();
  return useMutation({
    mutationKey: ["private", data?.person?.id, "sign-out"],
    mutationFn: () =>
      request<void>(
        `/api/identity/${everywhere ? "sign-out-everywhere" : "sign-out"}`,
        { body: {} },
      ),
    onSuccess: async () => {
      if (
        client.getQueryData<Status>(statusKey)?.person?.id !== data?.person?.id
      )
        return;
      await client.cancelQueries();
      clearPrivateQueries(client);
      client.setQueryData<Status>(statusKey, {
        claimed: true,
        auth_mode: data?.auth_mode ?? "google",
      });
    },
  });
}

export function useFakeSignIn() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (claims: SignInRequest) =>
      request<Person>("/api/identity/fake-sign-in", { body: claims }),
    onSuccess: async (person) => {
      await client.cancelQueries();
      clearPrivateQueries(client);
      client.setQueryData<Status>(statusKey, {
        claimed: true,
        person,
        auth_mode: "fake",
      });
    },
  });
}
