import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useMemo } from "react";

import { request } from "../../lib/http";
import type {
  ViewerAlbum,
  ViewerDay,
  ViewerEntry,
  ViewerLibrary,
  ViewerPage,
} from "../../types/generated/publishing";
import { usePrivateScope } from "./people";

// An omitted Album selects the cross-album library, which has no preview mode.
// personID selects a Curator's preview as that Person; offered selects the
// viewer's own preview of an Album offered to them.
export type ViewerContext =
  | { albumID: string; personID?: string; offered?: never }
  | { albumID: string; personID?: never; offered: true }
  | { albumID?: never; personID?: never; offered?: never };

// Galleries load every page, so refetching them on each window focus would
// replay the whole Album. Keys already name the Person, so nothing leaks
// between people while pages stay cached.
const viewerStaleTime = 5 * 60_000;
export type ViewerTab = "photos" | "videos";

function galleryURL({ albumID, personID, offered }: ViewerContext) {
  if (albumID === undefined) return "/api/library";
  const id = encodeURIComponent(albumID);
  if (offered) return `/api/albums/${id}/preview`;
  return personID === undefined
    ? `/api/albums/${id}`
    : `/api/curator/albums/${id}/preview/${encodeURIComponent(personID)}`;
}

function contextKey({ albumID, personID, offered }: ViewerContext) {
  return [
    "viewer",
    offered ? "offered" : personID === undefined ? "member" : "preview",
    albumID ?? "library",
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

// Albums offered to the viewer beyond their own, with counts and covers from
// the offered media only.
export function useMoreAlbums() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "viewer", "offered", "albums"],
    queryFn: ({ signal }) =>
      request<ViewerAlbum[]>("/api/albums/more", { signal }),
    retry: false,
  });
}

// Join places an offered Album among the viewer's own, and Leave takes the
// offered media back out. Either moves media between the viewer's Albums,
// More albums, and Library, so every viewer query is refreshed.
function useMembership(albumID: string, action: "join" | "leave") {
  const scope = usePrivateScope();
  const client = useQueryClient();
  return useMutation({
    mutationFn: () =>
      request(`/api/albums/${encodeURIComponent(albumID)}/${action}`, {
        body: {},
      }),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: [...scope, "viewer"] }),
  });
}

export function useJoinAlbum(albumID: string) {
  return useMembership(albumID, "join");
}

export function useLeaveAlbum(albumID: string) {
  return useMembership(albumID, "leave");
}

export function useViewerGallery(context: ViewerContext) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, ...contextKey(context)],
    queryFn: ({ signal }) =>
      request<ViewerAlbum | ViewerLibrary>(galleryURL(context), { signal }),
    staleTime: viewerStaleTime,
    retry: false,
  });
}

// A gallery page carries at most this many entries, matching the API.
const pageSize = 500;

export function dayCount(day: ViewerDay, tab: ViewerTab) {
  return tab === "photos" ? day.photo_count : day.video_count;
}

// viewerRanges cuts the capture days with media on a tab into runs that each
// fit one page, so a large Album loads every run at once instead of page by
// page from the top. Bounds are omitted where nothing lies beyond them, so a
// small Album asks for the whole gallery with no parameters.
export function viewerRanges(
  days: ViewerDay[],
  tab: ViewerTab,
  newestFirst = false,
) {
  const runs: ViewerDay[][] = [];
  // Date bounds are always ascending and end-exclusive, even when the
  // gallery displays the resulting runs and their days newest first.
  const ascending = newestFirst ? [...days].reverse() : days;
  for (const day of ascending) {
    if (dayCount(day, tab) === 0) continue;
    const run = runs.at(-1);
    const total = run?.reduce((sum, item) => sum + dayCount(item, tab), 0) ?? 0;
    if (run && total + dayCount(day, tab) <= pageSize) run.push(day);
    else runs.push([day]);
  }
  const ranges = runs.map((run, index) => ({
    from: index === 0 ? "" : run[0].date,
    to: runs[index + 1]?.[0].date ?? "",
    days: newestFirst ? [...run].reverse() : run,
  }));
  return newestFirst ? ranges.reverse() : ranges;
}

export function useViewerEntries(
  context: ViewerContext,
  tab: ViewerTab,
  days: ViewerDay[],
) {
  const scope = usePrivateScope();
  const newestFirst = context.albumID === undefined;
  const ranges = useMemo(
    () => viewerRanges(days, tab, newestFirst),
    [days, tab, newestFirst],
  );
  const results = useQueries({
    queries: ranges.map((range) => ({
      queryKey: [...scope, ...contextKey(context), tab, range.from, range.to],
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const entries: ViewerEntry[] = [];
        let cursor = "";
        do {
          const params = new URLSearchParams();
          if (range.from) params.set("from", range.from);
          if (range.to) params.set("to", range.to);
          if (cursor) params.set("cursor", cursor);
          const query = params.toString();
          const page = await request<ViewerPage>(
            `${galleryURL(context)}/${tab}${query ? `?${query}` : ""}`,
            { signal },
          );
          entries.push(...page.entries);
          cursor = page.next_cursor;
        } while (cursor);
        return entries;
      },
      staleTime: viewerStaleTime,
      retry: false,
    })),
  });
  return ranges.map((range, index) => ({ ...range, result: results[index] }));
}
