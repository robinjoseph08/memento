import { useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { ViewerAlbum, ViewerPage } from "../../types/generated/publishing";
import { usePrivateScope } from "./people";

export type ViewerContext = { albumID: string; personID?: string };

// Galleries load every page, so refetching them on each window focus would
// replay the whole Album. Keys already name the Person, so nothing leaks
// between identities while pages stay cached.
const viewerStaleTime = 5 * 60_000;
export type ViewerTab = "photos" | "videos";

function albumURL({ albumID, personID }: ViewerContext) {
  const id = encodeURIComponent(albumID);
  return personID === undefined
    ? `/api/albums/${id}`
    : `/api/curator/albums/${id}/preview/${encodeURIComponent(personID)}`;
}

function contextKey({ albumID, personID }: ViewerContext) {
  return [
    "viewer",
    personID === undefined ? "member" : "preview",
    albumID,
    personID ?? "",
  ] as const;
}

export function useViewerAlbums() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "viewer", "member", "albums"],
    queryFn: ({ signal }) => request<ViewerAlbum[]>("/api/albums", { signal }),
    retry: false,
  });
}

export function useViewerAlbum(context: ViewerContext) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, ...contextKey(context)],
    queryFn: ({ signal }) =>
      request<ViewerAlbum>(albumURL(context), { signal }),
    staleTime: viewerStaleTime,
    retry: false,
  });
}

export function useViewerEntries(context: ViewerContext, tab: ViewerTab) {
  const scope = usePrivateScope();
  return useInfiniteQuery({
    queryKey: [...scope, ...contextKey(context), tab],
    initialPageParam: "",
    queryFn: ({ signal, pageParam }) =>
      request<ViewerPage>(
        `${albumURL(context)}/${tab}${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ""}`,
        { signal },
      ),
    getNextPageParam: (page) => page.next_cursor || undefined,
    staleTime: viewerStaleTime,
    retry: false,
  });
}
