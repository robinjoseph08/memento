import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import { statusKey } from "../../lib/query-client";
import type {
  BrowserSession,
  Profile,
  Status,
  UpdateProfileRequest,
} from "../../types/generated/identity";
import { usePrivateScope } from "./people";

export function useProfile() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "profile"],
    queryFn: ({ signal }) =>
      request<Profile>("/api/identity/profile", { signal }),
    retry: false,
  });
}
export function useSessions() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "sessions"],
    queryFn: ({ signal }) =>
      request<BrowserSession[]>("/api/identity/sessions", { signal }),
    retry: false,
  });
}
export function useUpdateProfile() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: UpdateProfileRequest) =>
      request<Profile>("/api/identity/profile", { body }),
    onSuccess: async (profile) => {
      const status = client.getQueryData<Status>(statusKey);
      if (!status || !scope[1] || status.person?.id !== scope[1]) return;
      client.setQueryData([...scope, "profile"], profile);
      client.setQueryData<Status>(statusKey, {
        ...status,
        person: profile.person,
      });
      await client.invalidateQueries({ queryKey: [...scope, "people"] });
      await client.invalidateQueries({
        queryKey: [...scope, "person", profile.person.id],
      });
    },
  });
}
export function useUnlinkIdentity() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (id: string) =>
      request<void>(`/api/identity/identities/${id}/unlink`, { body: {} }),
    onSuccess: async () => {
      if (client.getQueryData<Status>(statusKey)?.person?.id !== scope[1])
        return;
      await client.invalidateQueries({ queryKey: statusKey });
      await client.invalidateQueries({ queryKey: scope });
    },
  });
}
