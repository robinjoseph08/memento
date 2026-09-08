import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";

import type { Status } from "../types/generated/identity";
import { HTTPError } from "./http";

export const statusKey = ["identity", "status"] as const;

// Cancel before removing so late responses cannot repopulate private caches.
export function clearPrivateQueries(client: QueryClient) {
  const filters = {
    predicate: (query: { queryKey: readonly unknown[] }) =>
      query.queryKey[0] !== "identity" || query.queryKey[1] !== "status",
  };
  void client.cancelQueries(filters);
  client.removeQueries(filters);
  client.getMutationCache().clear();
}

export function createQueryClient() {
  function refreshAfterUnauthorized(
    error: unknown,
    scope?: readonly unknown[],
  ) {
    if (!(error instanceof HTTPError) || error.status !== 401) return;
    const status = client.getQueryData<Status>(statusKey);
    if (
      !status?.person ||
      (scope?.[0] === "private" && scope[1] !== status.person.id)
    )
      return;
    void client.cancelQueries();
    clearPrivateQueries(client);
    client.setQueryData<Status>(statusKey, { ...status, person: undefined });
    void client.invalidateQueries({ queryKey: statusKey });
  }
  const client = new QueryClient({
    queryCache: new QueryCache({
      onError: (error, query) =>
        refreshAfterUnauthorized(error, query.queryKey),
    }),
    mutationCache: new MutationCache({
      onError: (error, _variables, _result, mutation) =>
        refreshAfterUnauthorized(error, mutation.options.mutationKey),
    }),
  });
  return client;
}
