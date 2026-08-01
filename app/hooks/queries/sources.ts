import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type { SourceMediaResponse } from "../../types/generated/events";
import type {
  Album,
  DiscoveryResponse,
  ListResponse,
  ReconciliationResponse,
} from "../../types/generated/sources";
import { sourceKeys } from "./curationKeys";

export type SourceDisposition = "unreviewed" | "drafted" | "ignored";

export function useSources(
  identityGeneration: string,
  disposition: SourceDisposition,
) {
  return useInfiniteQuery({
    queryKey: sourceKeys.list(identityGeneration, disposition),
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
        queryKey: sourceKeys.all(identityGeneration),
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
        queryKey: sourceKeys.all(identityGeneration),
      });
    },
  });
}

export function useSourceMedia(
  identityGeneration: string,
  sourceIDs: string[],
) {
  const query = useQuery({
    queryKey: sourceKeys.mediaSelection(identityGeneration, sourceIDs),
    queryFn: async (): Promise<SourceMediaResponse> => {
      const byID = new Map<
        string,
        {
          item: SourceMediaResponse["media_items"][number];
          itemIndex: number;
          sourceIndex: number;
        }
      >();
      let nextIndex = 0;
      let stopped = false;
      const loadNext = async () => {
        while (!stopped && nextIndex < sourceIDs.length) {
          const sourceIndex = nextIndex;
          nextIndex += 1;
          const response = await apiJSON<SourceMediaResponse>(
            `/api/sources/${sourceIDs[sourceIndex]}/media-items`,
          );
          if (stopped) return;
          for (const [itemIndex, item] of response.media_items.entries()) {
            if (!byID.has(item.id))
              byID.set(item.id, { item, itemIndex, sourceIndex });
            if (byID.size > 100_000) {
              stopped = true;
              throw new Error(
                "Choose fewer Source albums or a smaller Media selection. A draft can include no more than 100,000 Media items.",
              );
            }
          }
        }
      };
      await Promise.all(
        Array.from({ length: Math.min(4, sourceIDs.length) }, () => loadNext()),
      );
      return {
        media_items: [...byID.values()]
          .sort(
            (left, right) =>
              left.sourceIndex - right.sourceIndex ||
              left.itemIndex - right.itemIndex,
          )
          .map(({ item }) => item),
      };
    },
    enabled: sourceIDs.length > 0,
    retry: false,
  });
  return {
    ...query,
    mediaItems: query.data?.media_items ?? [],
  };
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
