import { useMemo } from "react";

import { useRecipientMedia } from "../hooks/queries/recipientLibrary";
import type { Media } from "../types/generated/library";
import type { SessionResponse } from "../types/generated/setup";
import { SubsetArchiveControls } from "./ArchiveControls";
import { DateNavigation } from "./DateNavigation";
import { MediaGallery } from "./MediaGallery";
import { classifyRefreshedMedia, mediaDateLabel } from "./mediaPresentation";
import { LibraryError } from "./presentation";
import type { Destination, OpenMedia } from "./types";
import { useSubsetArchiveModel } from "./useSubsetArchiveModel";

export type PhotosSelection = {
  enabled: boolean;
  selectedMedia: Set<string>;
  revision: number;
  begin: () => void;
  clear: () => void;
  toggle: (item: Media) => void;
};

export function PhotosDestination({
  destination,
  session,
  onOpenMedia,
  selection,
}: {
  destination: Extract<Destination, "photos" | "favorites">;
  session: SessionResponse;
  onOpenMedia: OpenMedia;
  selection: PhotosSelection;
}) {
  const photos = useRecipientMedia(session.csrf_token, destination);
  const media = useMemo(
    () => photos.data?.pages.flatMap((page) => page.media) ?? [],
    [photos.data],
  );
  const dates = useMemo(() => [...new Set(media.map(mediaDateLabel))], [media]);
  const subsetArchive = useSubsetArchiveModel(
    session.csrf_token,
    selection.selectedMedia,
    selection.revision,
  );

  async function refreshListingAccess(mediaID: string) {
    const refreshed = await photos.refetch();
    if (refreshed.error) return "access-unconfirmed" as const;
    const current = refreshed.data?.pages
      .flatMap((page) => page.media)
      .find((item) => item.id === mediaID);
    return classifyRefreshedMedia(current);
  }

  return (
    <div className="photo-library-layout">
      {dates.length ? <DateNavigation dates={dates} /> : null}
      <div className="dated-galleries">
        <LibraryError error={photos.error} />
        {destination === "photos" ? (
          <SubsetArchiveControls
            csrfToken={session.csrf_token}
            enabled={selection.enabled}
            model={subsetArchive}
            onBegin={selection.begin}
            onCancel={selection.clear}
            publicComputer={session.session_type === "public"}
            selectedMedia={selection.selectedMedia}
          />
        ) : null}
        {dates.map((date, index) => (
          <section id={`date-${index}`} key={date}>
            <h2>{date}</h2>
            <MediaGallery
              media={media.filter((item) => mediaDateLabel(item) === date)}
              onOpen={(item) =>
                onOpenMedia(item, () => refreshListingAccess(item.id))
              }
              onToggle={selection.toggle}
              selected={selection.selectedMedia}
              selectionDisabled={subsetArchive.isPending}
              selectionEnabled={destination === "photos" && selection.enabled}
            />
          </section>
        ))}
        {!photos.isPending && !photos.error && media.length === 0 ? (
          <p className="library-empty">
            {destination === "favorites"
              ? "No Favorites yet."
              : "No photos are available."}
          </p>
        ) : null}
        {photos.hasNextPage ? (
          <button
            disabled={photos.isFetchingNextPage}
            onClick={() => void photos.fetchNextPage()}
            type="button"
          >
            {photos.isFetchingNextPage ? "Loading…" : "Load more photos"}
          </button>
        ) : null}
      </div>
    </div>
  );
}
