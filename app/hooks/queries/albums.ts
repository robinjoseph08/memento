import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import { statusKey } from "../../lib/query-client";
import type { Status } from "../../types/generated/identity";
import type {
  Album,
  AlbumDetail,
  ImportRequest,
  SourcePage,
  UpdateAlbumRequest,
} from "../../types/generated/publishing";
import { usePrivateScope } from "./people";

export function useAlbums(search = "") {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "albums", search],
    queryFn: ({ signal }) =>
      request<Album[]>(
        search
          ? `/api/curator/albums?q=${encodeURIComponent(search)}`
          : "/api/curator/albums",
        { signal },
      ),
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.some((album) =>
        ["queued", "processing", "interrupted"].includes(album.status),
      )
        ? 2000
        : false,
  });
}

export function useAlbum(id: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "album", id],
    queryFn: ({ signal }) =>
      request<AlbumDetail>(`/api/curator/albums/${encodeURIComponent(id)}`, {
        signal,
      }),
    retry: false,
    refetchInterval: (query) =>
      ["queued", "processing", "interrupted"].includes(
        query.state.data?.status ?? "",
      )
        ? 2000
        : false,
  });
}

function useAlbumCache() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return {
    scope,
    save: async (album: AlbumDetail) => {
      if (client.getQueryData<Status>(statusKey)?.person?.id !== scope[1])
        return;
      client.setQueryData([...scope, "album", album.id], album);
      await Promise.all([
        client.invalidateQueries({ queryKey: [...scope, "albums"] }),
        client.invalidateQueries({ queryKey: [...scope, "sources"] }),
      ]);
    },
  };
}

export function useImportAlbum() {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: ImportRequest) =>
      request<AlbumDetail>("/api/curator/imports", { body }),
    onSuccess: cache.save,
  });
}

export function useUpdateAlbum(id: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: UpdateAlbumRequest) =>
      request<AlbumDetail>(`/api/curator/albums/${encodeURIComponent(id)}`, {
        body,
      }),
    onSuccess: cache.save,
  });
}

export function useRetryAlbum(id: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: () =>
      request<AlbumDetail>(
        `/api/curator/albums/${encodeURIComponent(id)}/retry`,
        { body: {} },
      ),
    onSuccess: cache.save,
  });
}

export function useSources(search: string, page: number) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "sources", search, page],
    queryFn: ({ signal }) =>
      request<SourcePage>(
        `/api/curator/sources?q=${encodeURIComponent(search)}&page=${page}`,
        { signal },
      ),
    retry: false,
  });
}
