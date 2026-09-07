import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  Person,
  SignInRequest,
  Status,
} from "../../types/generated/identity";

export function useIdentityStatus() {
  return useQuery({
    queryKey: ["identity", "status"],
    queryFn: ({ signal }) =>
      request<Status>("/api/identity/status", { signal }),
    retry: false,
    staleTime: 30_000,
  });
}

export function useSignOut() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => request<void>("/api/identity/sign-out", { body: {} }),
    onSuccess: async () => {
      await client.cancelQueries();
      client.removeQueries({ queryKey: ["connection"] });
      client.setQueryData<Status>(["identity", "status"], {
        claimed: true,
        auth_mode: "fake",
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
      client.removeQueries({ queryKey: ["connection"] });
      client.setQueryData<Status>(["identity", "status"], {
        claimed: true,
        person,
        auth_mode: "fake",
      });
    },
  });
}
