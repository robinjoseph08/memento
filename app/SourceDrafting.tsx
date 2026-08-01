import { useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  useCreateEventDraft,
  useCreateLooseItem,
} from "./hooks/queries/events";
import { useSourceMedia } from "./hooks/queries/sources";
import { ErrorMessage } from "./presentation";
import type {
  CreateEventRequest,
  CreateLooseItemRequest,
  MediaItem,
} from "./types/generated/events";
import type { Album } from "./types/generated/sources";

type DraftKind = "event" | "loose";
type MediaScope = "all" | "subset";
type DraftPanel = "sources" | "media" | "details";

function defaultTimezone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

function useMobileDraftLayout() {
  const isMobile = () => window.innerWidth <= 864;
  const [mobile, setMobile] = useState(isMobile);
  useEffect(() => {
    const sync = () => setMobile(isMobile());
    sync();
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, []);
  return mobile;
}

function usableCaptureDateTime(value: string) {
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})(?:T| )(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})?$/,
  );
  if (!match) return false;
  const [year, month, day, hour, minute, second] = match.slice(1).map(Number);
  if (year < 1 || hour > 23 || minute > 59 || second > 59) return false;
  const offset = value.match(/[+-](\d{2}):(\d{2})$/);
  if (offset && (Number(offset[1]) > 24 || Number(offset[2]) > 59))
    return false;
  const parsed = new Date(Date.UTC(year, month - 1, day));
  return (
    parsed.getUTCFullYear() === year &&
    parsed.getUTCMonth() === month - 1 &&
    parsed.getUTCDate() === day
  );
}

function mediaDescription(item: MediaItem) {
  const kind = item.media_type === "video" ? "video" : "photo";
  if (
    !item.local_date_time ||
    !usableCaptureDateTime(item.local_date_time.trim())
  )
    return `Undated ${kind} Media ${item.id}, manual placement required`;
  return `${kind === "photo" ? "Photo" : "Video"} Media ${item.id}, captured ${item.local_date_time}`;
}

export function SourceDraftBuilder({
  albums,
  csrfToken,
  onClose,
}: {
  albums: Album[];
  csrfToken: string;
  onClose: () => void;
}) {
  const [, setSearchParams] = useSearchParams();
  const mobileLayout = useMobileDraftLayout();
  const [activePanel, setActivePanel] = useState<DraftPanel>("sources");
  const [kind, setKind] = useState<DraftKind>("event");
  const [scope, setScope] = useState<MediaScope>("all");
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [timezone, setTimezone] = useState(defaultTimezone);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState("");
  const [visibleMediaCount, setVisibleMediaCount] = useState(200);
  const eventIdempotencyKeyRef = useRef(crypto.randomUUID());
  const sourceIDs = useMemo(() => albums.map((album) => album.id), [albums]);
  const needsMediaSelection = kind === "loose" || scope === "subset";
  const media = useSourceMedia(csrfToken, needsMediaSelection ? sourceIDs : []);
  const createEvent = useCreateEventDraft(csrfToken);
  const createLoose = useCreateLooseItem(csrfToken);
  const sourcesRef = useRef<HTMLElement>(null);
  const mediaRef = useRef<HTMLElement>(null);
  const detailsRef = useRef<HTMLElement>(null);

  const choosePanel = (panel: DraftPanel) => {
    setActivePanel(panel);
    window.requestAnimationFrame(() => {
      const refs = {
        sources: sourcesRef,
        media: mediaRef,
        details: detailsRef,
      };
      refs[panel].current?.focus();
    });
  };

  const chooseKind = (nextKind: DraftKind) => {
    setKind(nextKind);
    setSelectedMedia((current) => {
      if (nextKind !== "loose" || current.size <= 1) return current;
      const first = current.values().next().value;
      return new Set(first ? [first] : []);
    });
  };

  const toggleMedia = (mediaID: string) => {
    setStatus("");
    setSelectedMedia((current) => {
      if (kind === "loose")
        return current.has(mediaID) ? new Set() : new Set([mediaID]);
      const next = new Set(current);
      if (next.has(mediaID)) next.delete(mediaID);
      else next.add(mediaID);
      return next;
    });
  };

  const availableMediaIDs = useMemo(
    () => new Set(media.mediaItems.map((item) => item.id)),
    [media.mediaItems],
  );
  const effectiveSelectedMedia = useMemo(
    () =>
      new Set(
        [...selectedMedia].filter((mediaID) => availableMediaIDs.has(mediaID)),
      ),
    [availableMediaIDs, selectedMedia],
  );
  const canCreateEvent =
    kind === "event" &&
    timezone.trim() !== "" &&
    (scope === "all" ||
      (effectiveSelectedMedia.size > 0 && !media.isFetching && !media.error)) &&
    !createEvent.isPending;
  const canCreateLoose =
    kind === "loose" &&
    timezone.trim() !== "" &&
    effectiveSelectedMedia.size === 1 &&
    !media.isFetching &&
    !media.error &&
    !createLoose.isPending;
  const unusedCount = media.mediaItems.length - effectiveSelectedMedia.size;
  const visibleMediaItems = useMemo(
    () =>
      [
        ...media.mediaItems.filter((item) =>
          effectiveSelectedMedia.has(item.id),
        ),
        ...media.mediaItems.filter(
          (item) => !effectiveSelectedMedia.has(item.id),
        ),
      ].slice(0, visibleMediaCount),
    [effectiveSelectedMedia, media.mediaItems, visibleMediaCount],
  );

  const submit = () => {
    setStatus("");
    if (kind === "event") {
      if (!canCreateEvent) return;
      const request: CreateEventRequest = {
        source_album_ids: sourceIDs,
        idempotency_key: eventIdempotencyKeyRef.current,
        ...(scope === "subset"
          ? { media_item_ids: [...effectiveSelectedMedia] }
          : {}),
        timezone: timezone.trim(),
        title,
        description,
      };
      createEvent.mutate(request, {
        onSuccess: (event) => {
          setSearchParams(
            new URLSearchParams({ workspace: "drafts", event: event.id }),
          );
        },
      });
      return;
    }
    const mediaID = effectiveSelectedMedia.values().next().value;
    if (!canCreateLoose || !mediaID) return;
    const request: CreateLooseItemRequest = {
      media_item_id: mediaID,
      timezone: timezone.trim(),
      title,
      description,
    };
    createLoose.mutate(request, {
      onSuccess: (looseItem) => {
        setSearchParams(
          new URLSearchParams({
            workspace: "drafts",
            loose: looseItem.id,
          }),
        );
      },
    });
  };

  const creationError = createEvent.error ?? createLoose.error;
  const showPanel = (panel: DraftPanel) =>
    !mobileLayout || activePanel === panel;

  return (
    <section
      aria-labelledby="source-draft-title"
      className="source-drafting"
      data-layout={mobileLayout ? "drill-down" : "split-pane"}
    >
      <header className="source-drafting-header">
        <div>
          <p className="step-label">Private Curator work</p>
          <h3 id="source-draft-title">Draft Source Media</h3>
          <p>
            Build Events or Loose items without granting Recipient access or
            sending optional delivery.
          </p>
        </div>
        <button onClick={onClose} type="button">
          Close drafting
        </button>
      </header>
      <nav aria-label="Source drafting steps" className="source-draft-nav">
        {(["sources", "media", "details"] as DraftPanel[]).map((panel) => (
          <button
            aria-controls={`source-draft-${panel}`}
            aria-pressed={activePanel === panel}
            key={panel}
            onClick={() => choosePanel(panel)}
            type="button"
          >
            {panel === "sources"
              ? `Sources (${albums.length})`
              : panel === "media"
                ? `Media (${media.mediaItems.length})`
                : "Details"}
          </button>
        ))}
      </nav>
      <div className="source-draft-split" data-active-panel={activePanel}>
        {showPanel("sources") ? (
          <section
            className="source-draft-panel source-draft-sources"
            id="source-draft-sources"
            ref={sourcesRef}
            tabIndex={-1}
          >
            <h4>Selected Sources</h4>
            <p>
              Combine these Sources in one Event, or reuse them later to divide
              their Media across more Events.
            </p>
            <ul>
              {albums.map((album) => (
                <li key={album.id}>
                  <strong>{album.name}</strong>
                  <span>{album.asset_count} Source items</span>
                </li>
              ))}
            </ul>
            <button onClick={() => choosePanel("media")} type="button">
              Review Media
            </button>
          </section>
        ) : null}
        {showPanel("media") ? (
          <section
            className="source-draft-panel source-draft-media"
            id="source-draft-media"
            ref={mediaRef}
            tabIndex={-1}
          >
            <h4>Source Media</h4>
            {!needsMediaSelection ? (
              <p>
                Choose current Media in Draft details to select a subset, or
                keep all current and future Source Media.
              </p>
            ) : media.isPending ? (
              <p>Loading stable Media identities…</p>
            ) : null}
            <ErrorMessage error={media.error} />
            {needsMediaSelection &&
            !media.isPending &&
            !media.error &&
            media.mediaItems.length === 0 ? (
              <p>No available Media remains in these Sources.</p>
            ) : null}
            <ul className="source-media-list">
              {visibleMediaItems.map((item) => {
                const label = mediaDescription(item);
                return (
                  <li key={item.id}>
                    <label>
                      <input
                        aria-label={`Select ${label.charAt(0).toLowerCase()}${label.slice(1)}`}
                        checked={effectiveSelectedMedia.has(item.id)}
                        disabled={kind === "event" && scope === "all"}
                        name={kind === "loose" ? "loose-media" : undefined}
                        onChange={() => toggleMedia(item.id)}
                        type={kind === "loose" ? "radio" : "checkbox"}
                      />
                      <span>{label}</span>
                    </label>
                  </li>
                );
              })}
            </ul>
            {needsMediaSelection && !media.isFetching && !media.error ? (
              <p aria-live="polite">
                Showing {visibleMediaItems.length} of {media.mediaItems.length}{" "}
                available Media items.
              </p>
            ) : null}
            {visibleMediaItems.length < media.mediaItems.length ? (
              <button
                onClick={() => setVisibleMediaCount((count) => count + 200)}
                type="button"
              >
                Load more Media
              </button>
            ) : null}
            {kind === "event" && scope === "subset" ? (
              <p aria-live="polite">
                {unusedCount} {unusedCount === 1 ? "item" : "items"} will remain
                private and unused.
              </p>
            ) : null}
            <button onClick={() => choosePanel("details")} type="button">
              Review draft details
            </button>
          </section>
        ) : null}
        {showPanel("details") ? (
          <section
            className="source-draft-panel source-draft-details"
            id="source-draft-details"
            ref={detailsRef}
            tabIndex={-1}
          >
            <h4>Draft details</h4>
            <fieldset>
              <legend>Draft kind</legend>
              <label>
                <input
                  checked={kind === "event"}
                  name="draft-kind"
                  onChange={() => chooseKind("event")}
                  type="radio"
                />
                Event
              </label>
              <label>
                <input
                  checked={kind === "loose"}
                  name="draft-kind"
                  onChange={() => chooseKind("loose")}
                  type="radio"
                />
                Loose item
              </label>
            </fieldset>
            {kind === "event" ? (
              <fieldset>
                <legend>Event Media</legend>
                <label>
                  <input
                    checked={scope === "all"}
                    name="media-scope"
                    onChange={() => setScope("all")}
                    type="radio"
                  />
                  All current and future Source Media
                </label>
                <label>
                  <input
                    checked={scope === "subset"}
                    name="media-scope"
                    onChange={() => setScope("subset")}
                    type="radio"
                  />
                  Choose current Media
                </label>
              </fieldset>
            ) : (
              <p>
                Choose exactly one stable Media identity for this Loose item.
              </p>
            )}
            <label>
              Grouping timezone
              <input
                maxLength={100}
                onChange={(event) => setTimezone(event.target.value)}
                required
                spellCheck={false}
                type="text"
                value={timezone}
              />
            </label>
            <label>
              {kind === "event" ? "Event title" : "Loose item title"}
              <input
                maxLength={240}
                onChange={(event) => setTitle(event.target.value)}
                type="text"
                value={title}
              />
            </label>
            <label>
              {kind === "event"
                ? "Event description"
                : "Loose item description"}
              <textarea
                maxLength={2000}
                onChange={(event) => setDescription(event.target.value)}
                value={description}
              />
            </label>
            <ErrorMessage error={creationError} />
            <p aria-live="polite" role="status">
              {status}
            </p>
            <button
              className="source-primary-action"
              disabled={kind === "event" ? !canCreateEvent : !canCreateLoose}
              onClick={submit}
              type="button"
            >
              {kind === "event"
                ? createEvent.isPending
                  ? "Creating Event…"
                  : "Create private Event draft"
                : createLoose.isPending
                  ? "Creating Loose item…"
                  : "Create private Loose item"}
            </button>
          </section>
        ) : null}
      </div>
    </section>
  );
}
