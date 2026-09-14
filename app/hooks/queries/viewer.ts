import { useQueries, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

import { request } from "../../lib/http";
import type {
  ViewerAlbum,
  ViewerDay,
  ViewerEntry,
  ViewerPage,
} from "../../types/generated/publishing";
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

// A gallery page carries at most this many entries, matching the API.
const pageSize = 500;

export function dayCount(day: ViewerDay, tab: ViewerTab) {
  return tab === "photos" ? day.photo_count : day.video_count;
}

// viewerRanges cuts the capture days with media on a tab into runs that each
// fit one page, so a large Album loads every run at once instead of page by
// page from the top. Bounds are omitted where nothing lies beyond them, so a
// small Album asks for the whole gallery with no parameters.
export function viewerRanges(days: ViewerDay[], tab: ViewerTab) {
  const runs: ViewerDay[][] = [];
  for (const day of days) {
    if (dayCount(day, tab) === 0) continue;
    const run = runs.at(-1);
    const total = run?.reduce((sum, item) => sum + dayCount(item, tab), 0) ?? 0;
    if (run && total + dayCount(day, tab) <= pageSize) run.push(day);
    else runs.push([day]);
  }
  return runs.map((run, index) => ({
    from: index === 0 ? "" : run[0].date,
    to: runs[index + 1]?.[0].date ?? "",
    days: run,
  }));
}

export function useViewerEntries(
  context: ViewerContext,
  tab: ViewerTab,
  days: ViewerDay[],
) {
  const scope = usePrivateScope();
  const ranges = useMemo(() => viewerRanges(days, tab), [days, tab]);
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
            `${albumURL(context)}/${tab}${query ? `?${query}` : ""}`,
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
