import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type {
  Album,
  DiscoveryResponse,
  ListResponse,
  ReconciliationResponse,
} from "../../types/generated/sources";

export type SourceDisposition = "unreviewed" | "ignored";

export function useSources(
  identityGeneration: string,
  disposition: SourceDisposition,
) {
  return useInfiniteQuery({
    queryKey: ["sources", identityGeneration, disposition],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ disposition, limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<ListResponse>(`/api/sources?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
}

export function useDiscoverSources(identityGeneration: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiJSON<DiscoveryResponse>("/api/sources/discover", {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["sources", identityGeneration],
      });
    },
  });
}

export function useTriageSource(
  identityGeneration: string,
  album: Album,
  onSuccess: () => void,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiJSON<Album>(
        `/api/sources/${album.id}/${album.disposition === "ignored" ? "restore" : "ignore"}`,
        {
          method: "POST",
          headers: {
            "If-Match": `"${album.version}"`,
            "X-Memento-CSRF": identityGeneration,
          },
        },
      ),
    onSuccess: async () => {
      onSuccess();
      await queryClient.invalidateQueries({
        queryKey: ["sources", identityGeneration],
      });
    },
  });
}

export function useReconcileSource(
  identityGeneration: string,
  albumID: string,
  onSuccess: () => void,
) {
  return useMutation({
    mutationFn: () =>
      apiJSON<ReconciliationResponse>(`/api/sources/${albumID}/reconcile`, {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess,
  });
}
