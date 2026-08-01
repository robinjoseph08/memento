import { formatDateRange } from "../format";
import type { EventSummary } from "../types/generated/library";
import type { EventResult } from "../types/generated/search";
import { mediaAlt } from "./mediaPresentation";
import type { RecipientMedia } from "./types";

export function MediaGallery<T extends RecipientMedia>({
  media,
  onOpen,
  selected,
  selectionDisabled = false,
  selectionEnabled = false,
  onToggle,
}: {
  media: T[];
  onOpen: (media: T) => void;
  selected?: Set<string>;
  selectionDisabled?: boolean;
  selectionEnabled?: boolean;
  onToggle?: (media: T) => void;
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
          <button
            aria-label={`Open ${mediaAlt(item, index)}`}
            className="viewer-trigger"
            onClick={() => onOpen(item)}
            type="button"
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
          </button>
          {selectionEnabled && item.available ? (
            <label className="media-selection">
              <input
                aria-label={`${selected?.has(item.id) ? "Remove" : "Select"} ${mediaAlt(item, index)}`}
                checked={selected?.has(item.id) ?? false}
                disabled={selectionDisabled}
                onChange={() => {
                  if (!selectionDisabled) onToggle?.(item);
                }}
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

export function EventGallery<T extends EventSummary | EventResult>({
  events,
  onOpen,
  matching = false,
}: {
  events: T[];
  onOpen: (event: T) => void;
  matching?: boolean;
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
            <span>{formatDateRange(event.date_start, event.date_end)}</span>
            {event.place_labels?.length ? (
              <span>{event.place_labels.join(", ")}</span>
            ) : null}
            <span>
              {event.media_count} {matching ? "matching " : ""}
              {event.media_count === 1 ? "item" : "items"}
            </span>
          </button>
        );
      })}
    </div>
  );
}
