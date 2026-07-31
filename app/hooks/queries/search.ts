import { useQuery } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type {
  Request as SearchRequest,
  Response as SearchResponse,
} from "../../types/generated/search";

export function useRecipientSearch(
  identityGeneration: string,
  request: SearchRequest | null,
) {
  return useQuery({
    queryKey: ["recipient-search", identityGeneration, request],
    queryFn: () => {
      if (!request) throw new Error("Submit a Search first.");
      return apiJSON<SearchResponse>("/api/search", {
        method: "POST",
        body: JSON.stringify(request),
      });
    },
    enabled: request !== null,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
    retry: false,
  });
}
