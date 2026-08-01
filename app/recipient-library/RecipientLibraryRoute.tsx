import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { recordEngagement } from "../engagement";
import { ThemeToggle } from "../PWAControls";
import type { SessionResponse } from "../types/generated/setup";
import { EventDetailDestination, EventsDestination } from "./EventsDestination";
import { LibraryNavigation } from "./LibraryNavigation";
import { MediaViewer } from "./MediaViewer";
import { NewForYou } from "./NewForYou";
import { PhotosDestination, type PhotosSelection } from "./PhotosDestination";
import {
  captureDateFromSearch,
  captureDateSearch,
  destinationFromPath,
  destinationPath,
  eventIDFromPath,
} from "./routing";
import { SearchDestination } from "./SearchDestination";
import type {
  Destination,
  OpenedEvent,
  RecipientMedia,
  RefreshedMediaAccess,
} from "./types";
import { useNewForYouModel } from "./useNewForYouModel";
import { useSearchDestinationModel } from "./useSearchDestinationModel";

type OpenedMedia = {
  media: RecipientMedia;
  refreshListingAccess: () => Promise<RefreshedMediaAccess>;
};

export function RecipientLibraryRoute({
  session,
  pathname,
  search,
  navigatePath,
}: {
  session: SessionResponse;
  pathname: string;
  search: string;
  navigatePath: (pathname: string, replace?: boolean) => void;
}) {
  const destination = destinationFromPath(pathname);
  const selectedCaptureDate = captureDateFromSearch(search);
  const openedEventID = eventIDFromPath(pathname);
  const [openedEventSummary, setOpenedEventSummary] = useState<OpenedEvent>();
  const openedEvent = useMemo(
    () =>
      openedEventID
        ? openedEventSummary?.id === openedEventID
          ? openedEventSummary
          : { id: openedEventID }
        : undefined,
    [openedEventID, openedEventSummary],
  );
  const [openedMedia, setOpenedMedia] = useState<OpenedMedia>();
  const [routeSelection, setRouteSelection] = useState(() => ({
    enabled: false,
    selectedMedia: new Set<string>(),
    revision: 0,
  }));
  const searchModel = useSearchDestinationModel(session.csrf_token);
  const newForYouModel = useNewForYouModel(
    session.csrf_token,
    destination === "photos" && !openedEvent,
  );
  const mediaOpener = useRef<HTMLElement | null>(null);
  const libraryHeading = useRef<HTMLHeadingElement>(null);
  const navigationStarted = useRef(false);

  useEffect(() => {
    const title = openedEvent
      ? (openedEvent.title ?? "Event")
      : destination[0].toUpperCase() + destination.slice(1);
    document.title = `${title} | Memento`;
  }, [destination, openedEvent]);

  useEffect(() => {
    if (!navigationStarted.current) return;
    requestAnimationFrame(() => libraryHeading.current?.focus());
  }, [destination, openedEventID]);

  useEffect(() => {
    const recordVisit = () => {
      if (document.visibilityState === "visible") {
        void recordEngagement(session, { kind: "visit" });
      }
    };
    recordVisit();
    document.addEventListener("visibilitychange", recordVisit);
    return () => document.removeEventListener("visibilitychange", recordVisit);
  }, [session]);

  const resolveEventTitle = useCallback(
    (title: string) => {
      if (!openedEventID) return;
      setOpenedEventSummary((current) =>
        current?.id === openedEventID
          ? { ...current, title }
          : { id: openedEventID, title },
      );
    },
    [openedEventID],
  );

  function openEvent(summary: OpenedEvent) {
    navigationStarted.current = true;
    setOpenedEventSummary(summary);
    navigatePath(`/events/${encodeURIComponent(summary.id)}`);
    void recordEngagement(session, {
      kind: "event_opened",
      event_id: summary.id,
    });
  }

  function clearSelection() {
    setRouteSelection((current) => ({
      enabled: false,
      selectedMedia: new Set(),
      revision: current.revision + 1,
    }));
  }

  const selection: PhotosSelection = {
    enabled: routeSelection.enabled,
    selectedMedia: routeSelection.selectedMedia,
    revision: routeSelection.revision,
    begin: () => {
      setRouteSelection((current) => ({
        enabled: true,
        selectedMedia: new Set(),
        revision: current.revision + 1,
      }));
    },
    clear: clearSelection,
    toggle: (item) => {
      setRouteSelection((current) => {
        const selectedMedia = new Set(current.selectedMedia);
        if (selectedMedia.has(item.id)) selectedMedia.delete(item.id);
        else selectedMedia.add(item.id);
        return {
          ...current,
          selectedMedia,
          revision: current.revision + 1,
        };
      });
    },
  };

  function navigateTo(nextDestination: Destination) {
    navigationStarted.current = true;
    clearSelection();
    setOpenedEventSummary(undefined);
    navigatePath(destinationPath(nextDestination));
    void recordEngagement(session, {
      kind: "destination_opened",
      destination: nextDestination,
    });
  }

  function openMedia(
    media: RecipientMedia,
    refreshListingAccess: () => Promise<RefreshedMediaAccess>,
  ) {
    mediaOpener.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setOpenedMedia({ media, refreshListingAccess });
    void recordEngagement(session, {
      kind: "media_opened",
      media_item_id: media.id,
    });
  }

  function closeMedia() {
    const opener = mediaOpener.current;
    setOpenedMedia(undefined);
    requestAnimationFrame(() => {
      if (opener?.isConnected) opener.focus();
    });
  }

  return (
    <section aria-label="Recipient library" className="recipient-library">
      <LibraryNavigation
        className="library-rail"
        current={openedEvent ? undefined : destination}
        onNavigate={navigateTo}
        showBrand
        showSearch={false}
      />
      <div className="library-content">
        <div className="app-preferences">
          <ThemeToggle />
        </div>
        {openedEvent ? (
          <EventDetailDestination
            eventSummary={openedEvent}
            headingRef={libraryHeading}
            onBack={() => {
              navigationStarted.current = true;
              setOpenedEventSummary(undefined);
              navigatePath("/events");
            }}
            onOpenMedia={openMedia}
            onSearch={() => navigateTo("search")}
            onTitle={resolveEventTitle}
            session={session}
          />
        ) : (
          <>
            <header className="library-heading">
              <p className="step-label">Private family archive</p>
              <h1 ref={libraryHeading} tabIndex={-1}>
                {destination[0].toUpperCase() + destination.slice(1)}
              </h1>
              {destination === "favorites" ? (
                <p>Favorites aren&apos;t shared with other recipients.</p>
              ) : null}
              <button
                aria-current={destination === "search" ? "page" : undefined}
                aria-label="Search library"
                className="desktop-search-action"
                onClick={() => navigateTo("search")}
                type="button"
              >
                Search
              </button>
            </header>
            {destination === "photos" ? (
              <NewForYou model={newForYouModel} onOpenEvent={openEvent} />
            ) : null}
            {destination === "search" ? (
              <SearchDestination
                model={searchModel}
                onOpenEvent={openEvent}
                onOpenMedia={openMedia}
              />
            ) : destination === "events" ? (
              <EventsDestination
                csrfToken={session.csrf_token}
                onOpenEvent={openEvent}
              />
            ) : (
              <PhotosDestination
                destination={destination}
                key={destination}
                onOpenMedia={openMedia}
                onSelectCaptureDate={(captureDate, replace = false) =>
                  navigatePath(
                    `${destinationPath(destination)}${captureDateSearch(captureDate)}`,
                    replace,
                  )
                }
                selectedCaptureDate={selectedCaptureDate}
                selection={selection}
                session={session}
              />
            )}
          </>
        )}
      </div>
      {openedMedia ? (
        <MediaViewer
          media={openedMedia.media}
          onClose={closeMedia}
          onOriginalDownload={() => {
            void recordEngagement(session, {
              kind: "original_download_started",
              media_item_id: openedMedia.media.id,
            });
          }}
          onVideoStarted={() => {
            void recordEngagement(session, {
              kind: "video_started",
              media_item_id: openedMedia.media.id,
            });
          }}
          refreshListingAccess={openedMedia.refreshListingAccess}
          session={session}
        />
      ) : null}
      <LibraryNavigation
        className="mobile-library-nav"
        current={openedEvent ? undefined : destination}
        onNavigate={navigateTo}
      />
    </section>
  );
}
