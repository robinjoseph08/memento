import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  AlbumDetail,
  DeleteAlbumRequest,
  PublicationReview,
  PublishRequest,
} from "../../types/generated/publishing";
import { useAlbumCache } from "./albums";
import { usePrivateScope } from "./people";

export function usePublicationReview(albumID: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "publication", albumID],
    queryFn: ({ signal }) =>
      request<PublicationReview>(
        `/api/curator/albums/${encodeURIComponent(albumID)}/publication`,
        { signal },
      ),
    retry: false,
    refetchOnMount: "always",
  });
}

export function usePublishAlbum(albumID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: PublishRequest) =>
      request<AlbumDetail>(
        `/api/curator/albums/${encodeURIComponent(albumID)}/publish`,
        { body },
      ),
    onSuccess: cache.save,
  });
}

export function useUnpublishAlbum(albumID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: () =>
      request<AlbumDetail>(
        `/api/curator/albums/${encodeURIComponent(albumID)}/unpublish`,
        { body: {} },
      ),
    onSuccess: cache.save,
  });
}

export function useDeleteAlbum(albumID: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: DeleteAlbumRequest) =>
      request<Record<string, never>>(
        `/api/curator/albums/${encodeURIComponent(albumID)}/delete`,
        { body },
      ),
    onSuccess: async () => {
      client.removeQueries({ queryKey: [...scope, "album", albumID] });
      client.removeQueries({ queryKey: [...scope, "publication", albumID] });
      await Promise.all([
        client.invalidateQueries({ queryKey: [...scope, "albums"] }),
        client.invalidateQueries({ queryKey: [...scope, "sources"] }),
        client.invalidateQueries({ queryKey: [...scope, "viewer"] }),
      ]);
    },
  });
}
