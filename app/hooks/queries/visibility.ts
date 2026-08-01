import { useInfiniteQuery } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type {
  PeopleSearchRequest,
  PeopleSearchResponse,
} from "../../types/generated/visibility";

export function useRecipientPeopleSearch(
  identityGeneration: string,
  query: string,
) {
  return useInfiniteQuery({
    queryKey: ["recipient-people-search", identityGeneration, query],
    queryFn: ({ pageParam, signal }) =>
      apiJSON<PeopleSearchResponse>("/api/me/people/search", {
        method: "POST",
        signal,
        body: JSON.stringify({
          query,
          cursor: pageParam || undefined,
          limit: 25,
        } satisfies PeopleSearchRequest),
      }),
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    refetchInterval: 30_000,
    refetchOnReconnect: true,
    refetchOnWindowFocus: true,
    retry: false,
  });
}
