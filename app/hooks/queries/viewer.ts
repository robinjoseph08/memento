import { useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { ViewerAlbum, ViewerPage } from "../../types/generated/publishing";
import { usePrivateScope } from "./people";

export type ViewerContext = { albumID: string; personID?: string };
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
    gcTime: 0,
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
    gcTime: 0,
    retry: false,
  });
}
