import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  CreatePersonRequest,
  Person,
  PersonDetail,
  Preauthorization,
  PreauthorizeRequest,
  Status,
  UpdatePersonRequest,
} from "../../types/generated/identity";
import { useIdentityStatus } from "./identity";

export function usePrivateScope() {
  const { data } = useIdentityStatus();
  return ["private", data?.person?.id] as const;
}

export function usePeople(search: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "people", search],
    queryFn: ({ signal }) =>
      request<Person[]>(`/api/people?q=${encodeURIComponent(search)}`, {
        signal,
      }),
    retry: false,
  });
}

export function usePerson(id: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "person", id],
    queryFn: ({ signal }) =>
      request<PersonDetail>(`/api/people/${id}`, { signal }),
    retry: false,
  });
}

export function useCreatePerson() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: CreatePersonRequest) =>
      request<Person>("/api/people", { body }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "people"] }),
  });
}

export function useUpdatePerson(id: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: UpdatePersonRequest) =>
      request<Person>(`/api/people/${id}`, { body }),
    onSuccess: async (person) => {
      if (
        client.getQueryData<Status>(["identity", "status"])?.person?.id !==
        scope[1]
      )
        return;
      client.setQueryData<PersonDetail>([...scope, "person", id], (detail) =>
        detail ? { ...detail, person } : detail,
      );
      await client.invalidateQueries({ queryKey: scope });
      if (
        client.getQueryData<Status>(["identity", "status"])?.person?.id ===
        person.id
      )
        await client.invalidateQueries({ queryKey: ["identity", "status"] });
    },
  });
}

export function usePreauthorize(id: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: PreauthorizeRequest) =>
      request<Preauthorization>(`/api/people/${id}/preauthorizations`, {
        body,
      }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "person", id] }),
  });
}

export function useRevokePreauthorization(id: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (authorizationID: string) =>
      request<void>(
        `/api/people/${id}/preauthorizations/${authorizationID}/revoke`,
        { body: {} },
      ),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "person", id] }),
  });
}

export function useUnlinkPersonIdentity(id: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (identityID: string) =>
      request<void>(`/api/people/${id}/identities/${identityID}/unlink`, {
        body: {},
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: scope });
      await client.invalidateQueries({ queryKey: ["identity", "status"] });
    },
  });
}
