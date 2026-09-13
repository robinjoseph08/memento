import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import { statusKey } from "../../lib/query-client";
import type { Status } from "../../types/generated/identity";
import type {
  Album,
  AlbumAccessPreview,
  AlbumDetail,
  ImportRequest,
  MergeMomentsRequest,
  MoveEntriesRequest,
  RemoveAccessPreview,
  RemoveAccessPreviewRequest,
  RemoveAccessRequest,
  SaveAlbumAccessRequest,
  SaveRulesRequest,
  SetMomentCoverRequest,
  SourcePage,
  SplitMomentRequest,
  StructurePreview,
  UpdateAlbumRequest,
  UpdateMomentRequest,
  UpdateVideoRequest,
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

// Chapter extraction runs after import finishes, so the detail keeps polling
// while any video is still pending and stops once every result is in.
function chaptersPending(album: AlbumDetail | undefined) {
  return (
    album?.moments.some((moment) =>
      moment.entries.some((entry) => entry.chapter_status === "pending"),
    ) ?? false
  );
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
        : chaptersPending(query.state.data)
          ? 5000
          : false,
  });
}

export function useAlbumCache() {
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
        client.invalidateQueries({
          queryKey: [...scope, "publication", album.id],
        }),
        client.invalidateQueries({ queryKey: [...scope, "viewer"] }),
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

function momentURL(albumID: string, momentID: string, action = "") {
  const base = `/api/curator/albums/${encodeURIComponent(albumID)}/moments/${encodeURIComponent(momentID)}`;
  return action ? `${base}/${action}` : base;
}

export function useUpdateMoment(albumID: string, momentID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: UpdateMomentRequest) =>
      request<AlbumDetail>(momentURL(albumID, momentID), { body }),
    onSuccess: cache.save,
  });
}

export function useSetMomentCover(albumID: string, momentID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: SetMomentCoverRequest) =>
      request<AlbumDetail>(momentURL(albumID, momentID, "cover"), { body }),
    onSuccess: cache.save,
  });
}

// A refresh can take a while on a large Moment, so its response may predate a
// decision saved meanwhile. Invalidating instead of writing the result keeps
// the newer decision on screen.
export function useRefreshMomentFaces(albumID: string, momentID: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: () =>
      request<AlbumDetail>(momentURL(albumID, momentID, "faces/refresh"), {
        body: {},
      }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "album", albumID] }),
  });
}

function albumAccessURL(albumID: string, action = "") {
  const base = `/api/curator/albums/${encodeURIComponent(albumID)}/access`;
  return action ? `${base}/${action}` : base;
}

export function usePreviewAlbumAccess(albumID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: SaveAlbumAccessRequest) =>
      request<AlbumAccessPreview>(albumAccessURL(albumID, "preview"), { body }),
  });
}

export function useSaveAlbumAccess(albumID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: SaveAlbumAccessRequest) =>
      request<AlbumDetail>(albumAccessURL(albumID), { body }),
    onSuccess: cache.save,
  });
}

export function usePreviewRemoveAccess(albumID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: RemoveAccessPreviewRequest) =>
      request<RemoveAccessPreview>(
        albumAccessURL(albumID, "remove-all/preview"),
        {
          body,
        },
      ),
  });
}

export function useRemoveAllAccess(albumID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: RemoveAccessRequest) =>
      request<AlbumDetail>(albumAccessURL(albumID, "remove-all"), { body }),
    onSuccess: cache.save,
  });
}

export function useSetAccessRules(
  albumID: string,
  target: "moments" | "entries",
  targetID: string,
) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: SaveRulesRequest) =>
      request<AlbumDetail>(
        `/api/curator/albums/${encodeURIComponent(albumID)}/${target}/${encodeURIComponent(targetID)}/rules`,
        { body },
      ),
    onSuccess: cache.save,
  });
}

export function usePreviewMove(albumID: string, momentID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: MoveEntriesRequest) =>
      request<StructurePreview>(momentURL(albumID, momentID, "move/preview"), {
        body,
      }),
  });
}

export function useMoveEntries(albumID: string, momentID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: MoveEntriesRequest) =>
      request<AlbumDetail>(momentURL(albumID, momentID, "move"), { body }),
    onSuccess: cache.save,
  });
}

export function usePreviewSplit(albumID: string, momentID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: SplitMomentRequest) =>
      request<StructurePreview>(momentURL(albumID, momentID, "split/preview"), {
        body,
      }),
  });
}

export function useSplitMoment(albumID: string, momentID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: SplitMomentRequest) =>
      request<AlbumDetail>(momentURL(albumID, momentID, "split"), { body }),
    onSuccess: cache.save,
  });
}

export function usePreviewMerge(albumID: string, momentID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: MergeMomentsRequest) =>
      request<StructurePreview>(momentURL(albumID, momentID, "merge/preview"), {
        body,
      }),
  });
}

export function useMergeMoments(albumID: string, momentID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: MergeMomentsRequest) =>
      request<AlbumDetail>(momentURL(albumID, momentID, "merge"), { body }),
    onSuccess: cache.save,
  });
}

function entryURL(albumID: string, entryID: string, action: string) {
  return `/api/curator/albums/${encodeURIComponent(albumID)}/entries/${encodeURIComponent(entryID)}/${action}`;
}

export function useUpdateVideo(albumID: string, entryID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: UpdateVideoRequest) =>
      request<AlbumDetail>(entryURL(albumID, entryID, "video"), { body }),
    onSuccess: cache.save,
  });
}

export function useRetryChapters(albumID: string, entryID: string) {
  const cache = useAlbumCache();
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: () =>
      request<AlbumDetail>(entryURL(albumID, entryID, "chapters/retry"), {
        body: {},
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
