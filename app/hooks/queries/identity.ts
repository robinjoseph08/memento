import {
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";

import { HTTPError, request } from "../../lib/http";
import { clearPrivateQueries, statusKey } from "../../lib/query-client";
import type {
  Person,
  RequestSignInCodeRequest,
  SignInCodeResult,
  SignInRequest,
  Status,
  Tip,
  VerifySignInCodeRequest,
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
        auth_mode: data?.auth_mode ?? "",
        sign_in_methods: data?.sign_in_methods ?? [],
        sign_in_codes: data?.sign_in_codes ?? false,
        version: data?.version ?? "",
      });
    },
  });
}

// useRetireTip hides a Tip at once and records it for every device. A Tip
// retired twice, or by two requests at once, stays retired once. The status
// is read again afterwards, so a refresh that raced the retire, or a retire
// that failed, ends up showing what the server holds.
export function useRetireTip() {
  const client = useQueryClient();
  const retire = (person: Person, tips: Tip[]) => {
    client.setQueryData<Status>(statusKey, (status) =>
      status?.person?.id === person.id
        ? {
            ...status,
            person: {
              ...person,
              retired_tips: [
                ...new Set([...status.person.retired_tips, ...tips]),
              ],
            },
          }
        : status,
    );
  };
  return useMutation({
    mutationFn: (tip: Tip) =>
      request<Person>(`/api/identity/tips/${tip}/retire`, { body: {} }),
    onMutate: async (tip) => {
      await client.cancelQueries({ queryKey: statusKey });
      const person = client.getQueryData<Status>(statusKey)?.person;
      if (person) retire(person, [tip]);
    },
    onSuccess: (person) => retire(person, person.retired_tips),
    onSettled: () => client.invalidateQueries({ queryKey: statusKey }),
  });
}

// useResetTips brings back every Tip for the signed-in Person. Only
// development installations offer it.
export function useResetTips() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => request<Person>("/api/identity/tips/reset", { body: {} }),
    onSuccess: (person) =>
      client.setQueryData<Status>(statusKey, (status) =>
        status?.person?.id === person.id ? { ...status, person } : status,
      ),
  });
}

// signedIn replaces every private query with the new Person's status, which
// sends the page wherever that Person belongs.
async function signedIn(client: QueryClient, person: Person) {
  await client.cancelQueries();
  clearPrivateQueries(client);
  const previous = client.getQueryData<Status>(statusKey);
  client.setQueryData<Status>(statusKey, {
    claimed: true,
    person,
    auth_mode: previous?.auth_mode ?? "",
    sign_in_methods: previous?.sign_in_methods ?? [],
    sign_in_codes: previous?.sign_in_codes ?? false,
    version: previous?.version ?? "",
  });
}

export function useFakeSignIn() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (claims: SignInRequest) =>
      request<Person>("/api/identity/fake-sign-in", { body: claims }),
    onSuccess: (person) => signedIn(client, person),
  });
}

export function useRequestSignInCode() {
  return useMutation({
    mutationFn: async (body: RequestSignInCodeRequest) => {
      try {
        await request<void>("/api/identity/sign-in-code", { body });
      } catch (error) {
        // Server errors reach the browser redacted, so the one the Person
        // can act on is spelled out here.
        if (error instanceof HTTPError && error.status === 503)
          throw new HTTPError(
            "We couldn't send your code. Try again in a minute.",
            503,
          );
        throw error;
      }
    },
  });
}

export function useVerifySignInCode() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: VerifySignInCodeRequest) =>
      request<SignInCodeResult>("/api/identity/sign-in-code/verify", { body }),
    onSuccess: async (result) => {
      if (result.person) await signedIn(client, result.person);
    },
  });
}
