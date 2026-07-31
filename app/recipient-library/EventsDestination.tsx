import { useEffect, type RefObject } from "react";

import {
  useRecipientEvent,
  useRecipientEvents,
} from "../hooks/queries/recipientLibrary";
import type { SessionResponse } from "../types/generated/setup";
import { EventArchiveControls } from "./ArchiveControls";
import { EventGallery, MediaGallery } from "./MediaGallery";
import { classifyRefreshedMedia } from "./mediaPresentation";
import { LibraryError } from "./presentation";
import type { OpenedEvent, OpenMedia } from "./types";

export function EventsDestination({
  csrfToken,
  onOpenEvent,
}: {
  csrfToken: string;
  onOpenEvent: (event: OpenedEvent) => void;
}) {
  const events = useRecipientEvents(csrfToken);
  const items = events.data?.pages.flatMap((page) => page.events) ?? [];

  return (
    <>
      <LibraryError error={events.error} />
      <EventGallery events={items} onOpen={onOpenEvent} />
      {!events.isPending && !events.error && items.length === 0 ? (
        <p className="library-empty">No Events are available.</p>
      ) : null}
      {events.hasNextPage ? (
        <button
          disabled={events.isFetchingNextPage}
          onClick={() => void events.fetchNextPage()}
          type="button"
        >
          {events.isFetchingNextPage ? "Loading…" : "Load more Events"}
        </button>
      ) : null}
    </>
  );
}

export function EventDetailDestination({
  eventSummary,
  headingRef,
  onBack,
  onOpenMedia,
  onSearch,
  onTitle,
  session,
}: {
  eventSummary: OpenedEvent;
  headingRef: RefObject<HTMLHeadingElement | null>;
  onBack: () => void;
  onOpenMedia: OpenMedia;
  onSearch: () => void;
  onTitle: (title: string) => void;
  session: SessionResponse;
}) {
  const event = useRecipientEvent(session.csrf_token, eventSummary.id);
  const eventData = event.data?.pages[0];
  const media = event.data?.pages.flatMap((page) => page.media) ?? [];

  useEffect(() => {
    if (eventData?.title) onTitle(eventData.title);
  }, [eventData?.title, onTitle]);

  async function refreshListingAccess(mediaID: string) {
    const refreshed = await event.refetch();
    if (refreshed.error) return "access-unconfirmed" as const;
    const current = refreshed.data?.pages
      .flatMap((page) => page.media)
      .find((item) => item.id === mediaID);
    return classifyRefreshedMedia(current);
  }

  return (
    <>
      <button className="library-back" onClick={onBack} type="button">
        Back to Events
      </button>
      <EventArchiveControls
        csrfToken={session.csrf_token}
        eventID={eventSummary.id}
        heading={
          <>
            <h1 ref={headingRef} tabIndex={-1}>
              {eventData?.title ?? eventSummary.title ?? "Loading Event…"}
            </h1>
            {eventData?.description ? <p>{eventData.description}</p> : null}
            {eventData ? (
              <span>
                {eventData.media_count}{" "}
                {eventData.media_count === 1 ? "item" : "items"}
              </span>
            ) : null}
          </>
        }
        publicComputer={session.session_type === "public"}
        searchAction={
          <button
            aria-label="Search library"
            className="desktop-search-action"
            onClick={onSearch}
            type="button"
          >
            Search
          </button>
        }
      />
      <LibraryError error={event.error} />
      <MediaGallery
        media={media}
        onOpen={(item) =>
          onOpenMedia(item, () => refreshListingAccess(item.id))
        }
      />
      {event.hasNextPage ? (
        <button
          disabled={event.isFetchingNextPage}
          onClick={() => void event.fetchNextPage()}
          type="button"
        >
          {event.isFetchingNextPage ? "Loading…" : "Load more"}
        </button>
      ) : null}
    </>
  );
}
