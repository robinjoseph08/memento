import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";

import { apiJSON, apiNoContent } from "./api";
import type {
  PlanRequest as ArchivePlanRequest,
  PlanResponse as ArchivePlanResponse,
} from "./types/generated/archives";
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

function Gallery({
  media,
  onOpen,
  selected,
  selectionEnabled = false,
  onToggle,
}: {
  media: Media[];
  onOpen: (media: Media) => void;
  selected?: Set<string>;
  selectionEnabled?: boolean;
  onToggle?: (media: Media) => void;
}) {
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
            <button
              aria-label={`Open ${mediaAlt(item, index)}`}
              className="viewer-trigger"
              onClick={() => onOpen(item)}
              type="button"
            >
              <img
                alt={mediaAlt(item, index)}
                loading="lazy"
                src={item.thumbnail_url}
              />
            </button>
          ) : (
            <span className="media-unavailable">Source unavailable</span>
          )}
          {selectionEnabled && item.available ? (
            <label className="media-selection">
              <input
                aria-label={`${selected?.has(item.id) ? "Remove" : "Select"} ${mediaAlt(item, index)}`}
                checked={selected?.has(item.id) ?? false}
                onChange={() => onToggle?.(item)}
                type="checkbox"
              />
            </label>
          ) : null}
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

function MediaViewer({
  media,
  publicComputer,
  onClose,
}: {
  media: Media;
  publicComputer: boolean;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const viewerDialog: HTMLDialogElement = dialog;

    function containFocus(event: KeyboardEvent) {
      if (event.key !== "Tab") return;
      const focusable = viewerDialog.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), video[controls], [tabindex]:not([tabindex="-1"])',
      );
      const first = focusable.item(0);
      const last = focusable.item(focusable.length - 1);
      if (!first || !last) return;

      const active = document.activeElement;
      if (
        event.shiftKey &&
        (active === first || !viewerDialog.contains(active))
      ) {
        event.preventDefault();
        last.focus();
      } else if (
        !event.shiftKey &&
        (active === last || !viewerDialog.contains(active))
      ) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", containFocus, true);
    if (typeof viewerDialog.showModal === "function") {
      if (!viewerDialog.open) viewerDialog.showModal();
      return () => document.removeEventListener("keydown", containFocus, true);
    }

    if (!viewerDialog.open) viewerDialog.setAttribute("open", "");
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        viewerDialog.dispatchEvent(new Event("close"));
      }
    }
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("keydown", containFocus, true);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, []);

  function closeViewer() {
    const dialog = dialogRef.current;
    if (dialog && typeof dialog.close === "function") {
      dialog.close();
    } else {
      onClose();
    }
  }

  return (
    <dialog
      aria-label="Media viewer"
      aria-modal="true"
      className="media-viewer"
      onClose={onClose}
      ref={dialogRef}
    >
      <div className="viewer-media">
        {media.media_type === "video" ? (
          <video
            aria-label="Video preview"
            controls
            playsInline
            poster={media.thumbnail_url}
            preload="metadata"
            src={media.video_url}
          />
        ) : (
          <img alt="Selected photo preview" src={media.preview_url} />
        )}
      </div>
      <aside className="viewer-details">
        <button autoFocus onClick={closeViewer} type="button">
          Close viewer
        </button>
        <h2>{media.media_type === "video" ? "Video" : "Photo"}</h2>
        <dl>
          <dt>Date</dt>
          <dd>{mediaLabel(media)}</dd>
          <dt>Dimensions</dt>
          <dd>
            {media.width && media.height
              ? `${media.width} × ${media.height}`
              : "Unavailable"}
          </dd>
        </dl>
        {publicComputer ? (
          <p className="viewer-download-warning">
            This original will remain on this public computer after sign-out.
          </p>
        ) : null}
        <a className="viewer-download" download href={media.original_url}>
          Download original
        </a>
      </aside>
    </dialog>
  );
}

function ArchiveDownloads({
  plan,
  publicComputer,
}: {
  plan: ArchivePlanResponse;
  publicComputer: boolean;
}) {
  return (
    <section aria-label="Archive downloads" className="archive-downloads">
      <strong>{plan.name}</strong>
      <span>
        {plan.item_count} {plan.item_count === 1 ? "item" : "items"}, available
        for 15 minutes
      </span>
      {publicComputer ? (
        <p>
          These archive files will remain on this public computer after
          sign-out.
        </p>
      ) : null}
      <div>
        {plan.parts.map((part) => (
          <a
            download={part.filename}
            href={part.download_url}
            key={part.part_number}
          >
            Download{" "}
            {plan.parts.length === 1 ? "archive" : `part ${part.part_number}`}
          </a>
        ))}
      </div>
    </section>
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
  const [openedMedia, setOpenedMedia] = useState<Media>();
  const [selectionEnabled, setSelectionEnabled] = useState(false);
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [archivePlan, setArchivePlan] = useState<ArchivePlanResponse>();
  const mediaOpener = useRef<HTMLElement | null>(null);
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
  const archive = useMutation({
    mutationFn: (request: ArchivePlanRequest) =>
      apiJSON<ArchivePlanResponse>("/api/me/archives", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: (plan) => setArchivePlan(plan),
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
  const eventMedia = event.data?.pages.flatMap((page) => page.media) ?? [];
  const dates = useMemo(() => [...new Set(media.map(mediaLabel))], [media]);

  function openEvent(summary: EventSummary, isNew = false) {
    setArchivePlan(undefined);
    setOpenedEvent(summary);
    if (isNew) seen.mutate(summary.publication_id);
  }

  function toggleMedia(item: Media) {
    setArchivePlan(undefined);
    setSelectedMedia((current) => {
      const next = new Set(current);
      if (next.has(item.id)) next.delete(item.id);
      else next.add(item.id);
      return next;
    });
  }

  function planSubset() {
    archive.mutate({
      scope: "subset",
      event_id: null,
      media_ids: [...selectedMedia],
    });
  }

  function planEvent() {
    if (!openedEvent) return;
    archive.mutate({
      scope: "event",
      event_id: openedEvent.id,
      media_ids: [],
    });
  }

  function openMedia(item: Media) {
    mediaOpener.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setOpenedMedia(item);
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
      <nav aria-label="Library navigation" className="library-rail">
        <div className="library-brand">Memento</div>
        {(["photos", "events", "favorites"] as Destination[]).map((item) => (
          <button
            aria-current={
              !openedEvent && destination === item ? "page" : undefined
            }
            key={item}
            onClick={() => {
              setArchivePlan(undefined);
              setSelectionEnabled(false);
              setSelectedMedia(new Set());
              setOpenedEvent(undefined);
              setDestination(item);
            }}
            type="button"
          >
            {item[0].toUpperCase() + item.slice(1)}
          </button>
        ))}
      </nav>
      <div className="library-content">
        {openedEvent ? (
          <>
            <button
              className="library-back"
              onClick={() => {
                setArchivePlan(undefined);
                setOpenedEvent(undefined);
              }}
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
                <span>
                  {event.data.pages[0].media_count}{" "}
                  {event.data.pages[0].media_count === 1 ? "item" : "items"}
                </span>
              ) : null}
              <button
                disabled={archive.isPending}
                onClick={planEvent}
                type="button"
              >
                {archive.isPending ? "Preparing archive…" : "Download Event"}
              </button>
              {session.session_type === "public" ? (
                <p className="archive-warning">
                  Event archive files will remain on this public computer after
                  sign-out.
                </p>
              ) : null}
            </header>
            <LibraryError error={event.error ?? archive.error} />
            {archivePlan ? (
              <ArchiveDownloads
                plan={archivePlan}
                publicComputer={session.session_type === "public"}
              />
            ) : null}
            <Gallery media={eventMedia} onOpen={openMedia} />
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
                  <LibraryError error={photos.error ?? archive.error} />
                  {destination === "photos" ? (
                    <div className="selection-toolbar">
                      <button
                        onClick={() => {
                          setArchivePlan(undefined);
                          setSelectionEnabled((current) => !current);
                          setSelectedMedia(new Set());
                        }}
                        type="button"
                      >
                        {selectionEnabled
                          ? "Cancel selection"
                          : "Select photos"}
                      </button>
                      {selectionEnabled ? (
                        <button
                          disabled={
                            selectedMedia.size === 0 || archive.isPending
                          }
                          onClick={planSubset}
                          type="button"
                        >
                          {archive.isPending
                            ? "Preparing archive…"
                            : `Download ${selectedMedia.size} selected`}
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                  {destination === "photos" &&
                  selectionEnabled &&
                  session.session_type === "public" ? (
                    <p className="archive-warning">
                      Subset archive files will remain on this public computer
                      after sign-out.
                    </p>
                  ) : null}
                  {destination === "photos" && archivePlan ? (
                    <ArchiveDownloads
                      plan={archivePlan}
                      publicComputer={session.session_type === "public"}
                    />
                  ) : null}
                  {dates.map((date, index) => (
                    <section id={`date-${index}`} key={date}>
                      <h2>{date}</h2>
                      <Gallery
                        media={media.filter(
                          (item) => mediaLabel(item) === date,
                        )}
                        onOpen={openMedia}
                        onToggle={toggleMedia}
                        selected={selectedMedia}
                        selectionEnabled={
                          destination === "photos" && selectionEnabled
                        }
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
      {openedMedia ? (
        <MediaViewer
          media={openedMedia}
          onClose={closeMedia}
          publicComputer={session.session_type === "public"}
        />
      ) : null}
      <nav aria-label="Library navigation" className="mobile-library-nav">
        {(["photos", "events", "favorites"] as Destination[]).map((item) => (
          <button
            aria-current={
              !openedEvent && destination === item ? "page" : undefined
            }
            key={item}
            onClick={() => {
              setArchivePlan(undefined);
              setSelectionEnabled(false);
              setSelectedMedia(new Set());
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
