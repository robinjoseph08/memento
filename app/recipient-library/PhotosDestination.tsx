import { useEffect, useMemo, useRef, useState } from "react";

import {
  useRecipientChronology,
  useRecipientMediaWindow,
} from "../hooks/queries/recipientLibrary";
import { preferredScrollBehavior } from "../motion";
import type { Media } from "../types/generated/library";
import type { SessionResponse } from "../types/generated/setup";
import { SubsetArchiveControls } from "./ArchiveControls";
import { DateNavigation } from "./DateNavigation";
import { MediaGallery } from "./MediaGallery";
import { captureDateLabel, classifyRefreshedMedia } from "./mediaPresentation";
import { LibraryError } from "./presentation";
import type { Destination, OpenMedia } from "./types";
import { useSubsetArchiveModel } from "./useSubsetArchiveModel";

const UNDATED_KEY = "undated";

function captureDateKey(captureDate: string | null) {
  return captureDate ?? UNDATED_KEY;
}

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
  onSelectCaptureDate,
  selectedCaptureDate,
  session,
  onOpenMedia,
  selection,
}: {
  destination: Extract<Destination, "photos" | "favorites">;
  onSelectCaptureDate: (
    captureDate: string | null | undefined,
    replace?: boolean,
  ) => void;
  selectedCaptureDate: string | null | undefined;
  session: SessionResponse;
  onOpenMedia: OpenMedia;
  selection: PhotosSelection;
}) {
  const chronology = useRecipientChronology(session.csrf_token, destination);
  const chronologyDates = chronology.data?.dates ?? [];
  const exactTargetDate =
    selectedCaptureDate === undefined
      ? chronologyDates[0]
      : chronologyDates.find(
          (date) => date.capture_date === selectedCaptureDate,
        );
  const targetDate =
    exactTargetDate ??
    (selectedCaptureDate === null
      ? chronologyDates[0]
      : chronologyDates.find(
          (date) =>
            date.capture_date !== null &&
            typeof selectedCaptureDate === "string" &&
            date.capture_date <= selectedCaptureDate,
        )) ??
    chronologyDates.at(-1);
  const anchor = targetDate?.cursor ?? "";
  const photos = useRecipientMediaWindow(
    session.csrf_token,
    destination,
    anchor,
    chronology.isSuccess && targetDate !== undefined,
  );
  const media = useMemo(
    () =>
      targetDate
        ? (photos.data?.pages.flatMap((page) => page.media) ?? [])
        : [],
    [photos.data, targetDate],
  );
  const groups = useMemo(() => {
    const grouped = new Map<string | null, Media[]>();
    for (const item of media) {
      const group = grouped.get(item.capture_date);
      if (group) group.push(item);
      else grouped.set(item.capture_date, [item]);
    }
    return [...grouped.entries()].map(([captureDate, items]) => ({
      captureDate,
      items,
    }));
  }, [media]);
  const [visibility, setVisibility] = useState<{
    anchor: string;
    captureDate: string | null;
  }>();
  const lastScrolledAnchor = useRef<string | undefined>(undefined);
  const lastReconciledAnchor = useRef<string | undefined>(undefined);
  const subsetArchive = useSubsetArchiveModel(
    session.csrf_token,
    selection.selectedMedia,
    selection.revision,
  );

  useEffect(() => {
    if (
      selectedCaptureDate === undefined ||
      exactTargetDate ||
      !chronology.isSuccess
    ) {
      return;
    }
    onSelectCaptureDate(targetDate?.capture_date, true);
  }, [
    chronology.isSuccess,
    exactTargetDate,
    onSelectCaptureDate,
    selectedCaptureDate,
    targetDate,
  ]);

  useEffect(() => {
    if (!photos.isSuccess || !targetDate || !photos.data) return;
    const resolvedDate = photos.data.pages[0]?.media[0]?.capture_date;
    if (resolvedDate === targetDate.capture_date) {
      if (lastReconciledAnchor.current === anchor) {
        lastReconciledAnchor.current = undefined;
      }
      return;
    }
    if (lastReconciledAnchor.current === anchor) return;
    lastReconciledAnchor.current = anchor;
    void chronology.refetch();
  }, [anchor, chronology, photos.data, photos.isSuccess, targetDate]);

  useEffect(() => {
    const sections = groups
      .map(({ captureDate }) =>
        document.getElementById(`date-${captureDateKey(captureDate)}`),
      )
      .filter((section): section is HTMLElement => section !== null);
    if (sections.length === 0 || !("IntersectionObserver" in window)) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort(
            (left, right) =>
              Math.abs(left.boundingClientRect.top) -
              Math.abs(right.boundingClientRect.top),
          )[0];
        if (!(visible?.target instanceof HTMLElement)) return;
        const value = visible.target.dataset.captureDate;
        if (!value) return;
        setVisibility({
          anchor,
          captureDate: value === UNDATED_KEY ? null : value,
        });
      },
      { rootMargin: "-10% 0px -70%", threshold: 0 },
    );
    sections.forEach((section) => observer.observe(section));
    return () => observer.disconnect();
  }, [anchor, groups]);

  useEffect(() => {
    if (
      selectedCaptureDate === undefined ||
      !targetDate ||
      lastScrolledAnchor.current === anchor
    ) {
      return;
    }
    const section = document.getElementById(
      `date-${captureDateKey(targetDate.capture_date)}`,
    );
    if (!section) return;
    lastScrolledAnchor.current = anchor;
    section.scrollIntoView({ behavior: preferredScrollBehavior() });
  }, [anchor, groups, selectedCaptureDate, targetDate]);

  async function refreshListingAccess(mediaID: string) {
    const refreshed = await photos.refetch();
    if (refreshed.error) return "access-unconfirmed" as const;
    const current = refreshed.data?.pages
      .flatMap((page) => page.media)
      .find((item) => item.id === mediaID);
    return classifyRefreshedMedia(current);
  }

  const activeDate =
    visibility?.anchor === anchor
      ? visibility.captureDate
      : targetDate?.capture_date;
  const navigationBusy =
    chronology.isPending || (targetDate !== undefined && photos.isPending);

  return (
    <div className="photo-library-layout">
      {chronologyDates.length ? (
        <DateNavigation
          activeDate={activeDate}
          busy={navigationBusy}
          dates={chronologyDates}
          onSelect={onSelectCaptureDate}
        />
      ) : null}
      <div className="dated-galleries">
        <LibraryError
          error={chronology.error ?? (targetDate ? photos.error : null)}
        />
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
        {groups.map(({ captureDate, items }) => (
          <section
            data-capture-date={captureDateKey(captureDate)}
            id={`date-${captureDateKey(captureDate)}`}
            key={captureDateKey(captureDate)}
          >
            <h2>{captureDateLabel(captureDate)}</h2>
            <MediaGallery
              media={items}
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
        {chronology.isSuccess &&
        (chronologyDates.length === 0 ||
          (targetDate !== undefined &&
            photos.isSuccess &&
            media.length === 0)) ? (
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
