import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { apiJSON, apiNoContent } from "./api";
import type {
  Event as EventDetail,
  EventPage,
  EventSummary,
  Media,
  MediaPage,
  NewForYouResponse,
} from "./types/generated/library";
import type { SessionResponse } from "./types/generated/setup";

type Destination = "photos" | "events" | "favorites";

function mediaLabel(media: Media) {
  if (!media.local_date_time) return "Date unavailable";
  const parsed = new Date(media.local_date_time);
  if (Number.isNaN(parsed.valueOf())) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, {
    month: "long",
    year: "numeric",
  }).format(parsed);
}

function mediaAlt(item: Media, index: number) {
  const kind = item.media_type === "video" ? "Video" : "Photo";
  const date = mediaLabel(item);
  return date === "Date unavailable"
    ? `${kind} ${index + 1}, date unavailable`
    : `${kind} ${index + 1} from ${date}`;
}

function Gallery({ media }: { media: Media[] }) {
  return (
    <div aria-label="Media gallery" className="justified-gallery">
      {media.map((item, index) => (
        <figure
          className="gallery-item"
          key={item.id}
          style={{
            aspectRatio:
              item.width && item.height
                ? `${item.width} / ${item.height}`
                : "1",
            flexGrow: item.width && item.height ? item.width / item.height : 1,
          }}
        >
          {item.available ? (
            <img
              alt={mediaAlt(item, index)}
              loading="lazy"
              src={item.thumbnail_url}
            />
          ) : (
            <span className="media-unavailable">Source unavailable</span>
          )}
          {item.media_type === "video" ? (
            <span className="media-kind">Video</span>
          ) : null}
        </figure>
      ))}
    </div>
  );
}

function EventCards({
  events,
  onOpen,
}: {
  events: EventSummary[];
  onOpen: (event: EventSummary) => void;
}) {
  return (
    <div aria-label="Event gallery" className="event-gallery">
      {events.map((event) => {
        const ratio =
          event.cover_width && event.cover_height
            ? event.cover_width / event.cover_height
            : 1;
        return (
          <button
            className="event-card"
            key={event.id}
            onClick={() => onOpen(event)}
            style={{ flexBasis: `${ratio * 11}rem`, flexGrow: ratio }}
            type="button"
          >
            <span
              className="event-cover"
              style={{
                aspectRatio:
                  event.cover_width && event.cover_height
                    ? `${event.cover_width} / ${event.cover_height}`
                    : "1",
              }}
            >
              {event.cover_available ? (
                <img alt="" loading="lazy" src={event.thumbnail_url} />
              ) : (
                <span className="media-unavailable">Source unavailable</span>
              )}
            </span>
            <strong>{event.title}</strong>
            <span>
              {event.media_count} {event.media_count === 1 ? "item" : "items"}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function LibraryError({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

export function RecipientLibrary({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [destination, setDestination] = useState<Destination>("photos");
  const [openedEvent, setOpenedEvent] = useState<EventSummary>();
  const endpoint = destination === "favorites" ? "favorites" : "photos";
  const photos = useInfiniteQuery({
    queryKey: ["recipient-library", session.csrf_token, endpoint],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<MediaPage>(`/api/me/${endpoint}?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: destination !== "events" && !openedEvent,
    retry: false,
  });
  const events = useInfiniteQuery({
    queryKey: ["recipient-events", session.csrf_token],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "24" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventPage>(`/api/me/events?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: destination === "events" && !openedEvent,
    retry: false,
  });
  const newForYou = useQuery({
    queryKey: ["new-for-you", session.csrf_token],
    queryFn: () => apiJSON<NewForYouResponse>("/api/me/new-for-you"),
    enabled: destination === "photos" && !openedEvent,
    retry: false,
  });
  const event = useInfiniteQuery({
    queryKey: ["recipient-event", session.csrf_token, openedEvent?.id],
    queryFn: ({ pageParam }) => {
      if (!openedEvent) throw new Error("Choose an Event first.");
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventDetail>(
        `/api/me/events/${openedEvent.id}?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: Boolean(openedEvent),
    retry: false,
  });
  const seen = useMutation({
    mutationFn: (publicationID: string) =>
      apiNoContent(`/api/me/new-for-you/${publicationID}/seen`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["new-for-you", session.csrf_token],
      }),
  });
  const media = useMemo(
    () => photos.data?.pages.flatMap((page) => page.media) ?? [],
    [photos.data],
  );
  const eventItems = events.data?.pages.flatMap((page) => page.events) ?? [];
  const openedMedia = event.data?.pages.flatMap((page) => page.media) ?? [];
  const dates = useMemo(() => [...new Set(media.map(mediaLabel))], [media]);

  function openEvent(summary: EventSummary, isNew = false) {
    setOpenedEvent(summary);
    if (isNew) seen.mutate(summary.publication_id);
  }

  return (
    <section aria-label="Recipient library" className="recipient-library">
      <aside aria-label="Library navigation" className="library-rail">
        <div className="library-brand">Memento</div>
        {(["photos", "events", "favorites"] as Destination[]).map((item) => (
          <button
            aria-current={
              !openedEvent && destination === item ? "page" : undefined
            }
            key={item}
            onClick={() => {
              setOpenedEvent(undefined);
              setDestination(item);
            }}
            type="button"
          >
            {item[0].toUpperCase() + item.slice(1)}
          </button>
        ))}
      </aside>
      <div className="library-content">
        {openedEvent ? (
          <>
            <button
              className="library-back"
              onClick={() => setOpenedEvent(undefined)}
              type="button"
            >
              Back to {destination === "photos" ? "Photos" : "Events"}
            </button>
            <header className="library-heading">
              <h1>{event.data?.pages[0]?.title ?? openedEvent.title}</h1>
              {event.data?.pages[0]?.description ? (
                <p>{event.data.pages[0].description}</p>
              ) : null}
              {event.data?.pages[0] ? (
                <span>{event.data.pages[0].media_count} items</span>
              ) : null}
            </header>
            <LibraryError error={event.error} />
            <Gallery media={openedMedia} />
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
        ) : (
          <>
            <header className="library-heading">
              <p className="step-label">Private family archive</p>
              <h1>{destination[0].toUpperCase() + destination.slice(1)}</h1>
              {destination === "favorites" ? (
                <p>Favorites aren&apos;t shared with other recipients.</p>
              ) : null}
            </header>
            {destination === "photos" ? (
              <LibraryError error={newForYou.error ?? seen.error} />
            ) : null}
            {destination === "photos" && newForYou.data?.events.length ? (
              <section
                aria-labelledby="new-for-you-title"
                className="new-for-you"
              >
                <h2 id="new-for-you-title">New for you</h2>
                <EventCards
                  events={newForYou.data.events}
                  onOpen={(summary) => openEvent(summary, true)}
                />
              </section>
            ) : null}
            {destination === "events" ? (
              <>
                <LibraryError error={events.error} />
                <EventCards events={eventItems} onOpen={openEvent} />
                {!events.isPending &&
                !events.error &&
                eventItems.length === 0 ? (
                  <p className="library-empty">No Events are available.</p>
                ) : null}
                {events.hasNextPage ? (
                  <button
                    disabled={events.isFetchingNextPage}
                    onClick={() => void events.fetchNextPage()}
                    type="button"
                  >
                    {events.isFetchingNextPage
                      ? "Loading…"
                      : "Load more Events"}
                  </button>
                ) : null}
              </>
            ) : (
              <div className="photo-library-layout">
                {dates.length ? (
                  <>
                    <label className="mobile-date-nav">
                      Jump to date
                      <select
                        onChange={(change) =>
                          document
                            .getElementById(
                              `date-${change.target.selectedIndex}`,
                            )
                            ?.scrollIntoView({ behavior: "smooth" })
                        }
                      >
                        {dates.map((date) => (
                          <option key={date}>{date}</option>
                        ))}
                      </select>
                    </label>
                    <nav aria-label="Photo dates" className="date-rail">
                      {dates.map((date, index) => (
                        <a href={`#date-${index}`} key={date}>
                          {date}
                        </a>
                      ))}
                    </nav>
                  </>
                ) : null}
                <div className="dated-galleries">
                  <LibraryError error={photos.error} />
                  {dates.map((date, index) => (
                    <section id={`date-${index}`} key={date}>
                      <h2>{date}</h2>
                      <Gallery
                        media={media.filter(
                          (item) => mediaLabel(item) === date,
                        )}
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
                      {photos.isFetchingNextPage
                        ? "Loading…"
                        : "Load more photos"}
                    </button>
                  ) : null}
                </div>
              </div>
            )}
          </>
        )}
      </div>
      <nav aria-label="Library navigation" className="mobile-library-nav">
        {(["photos", "events", "favorites"] as Destination[]).map((item) => (
          <button
            aria-current={
              !openedEvent && destination === item ? "page" : undefined
            }
            key={item}
            onClick={() => {
              setOpenedEvent(undefined);
              setDestination(item);
            }}
            type="button"
          >
            {item[0].toUpperCase() + item.slice(1)}
          </button>
        ))}
      </nav>
    </section>
  );
}
