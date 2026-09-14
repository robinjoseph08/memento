import type { Album } from "../../types/generated/publishing";

// Every card is in exactly one of these states, each with the one action
// that moves it along.
export type AlbumState =
  "processing" | "failed" | "unpublished" | "ready" | "published";

export function albumState(album: Album): AlbumState {
  if (album.status === "queued" || album.status === "processing")
    return "processing";
  if (album.status !== "complete") return "failed";
  if (album.published) return "published";
  return album.ready ? "ready" : "unpublished";
}

export const stateLabels: Record<AlbumState, string> = {
  processing: "Import in progress",
  failed: "Import failed",
  unpublished: "Unpublished",
  ready: "Ready to publish",
  published: "Published",
};

// Import statuses that are still moving or need a retry keep the wording the
// Album editor uses for the same state.
export const importLabels: Record<string, string> = {
  queued: "Waiting to import",
  processing: "Import in progress",
  interrupted: "Import interrupted",
  failed: "Import failed",
};
