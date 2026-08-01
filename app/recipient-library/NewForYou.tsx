import type { EventSummary, LooseItem } from "../types/generated/library";
import { EventGallery } from "./MediaGallery";
import { mediaAlt } from "./mediaPresentation";
import { LibraryError } from "./presentation";
import type { OpenMedia } from "./types";
import type { NewForYouModel } from "./useNewForYouModel";

export function NewForYou({
  model,
  onOpenEvent,
  onOpenMedia,
}: {
  model: NewForYouModel;
  onOpenEvent: (event: EventSummary) => void;
  onOpenMedia: OpenMedia;
}) {
  const events = model.newForYou.data?.events ?? [];
  const looseItems = model.newForYou.data?.loose_items ?? [];
  if (!events.length && !looseItems.length)
    return <LibraryError error={model.newForYou.error ?? model.seen.error} />;
  return (
    <section aria-labelledby="new-for-you-title" className="new-for-you">
      <LibraryError error={model.newForYou.error ?? model.seen.error} />
      <h2 id="new-for-you-title">New for you</h2>
      {events.length ? (
        <EventGallery
          events={events}
          onOpen={(event) => {
            onOpenEvent(event);
            model.seen.mutate(event.publication_id);
          }}
        />
      ) : null}
      {looseItems.length ? (
        <div aria-label="Loose item gallery" className="loose-item-gallery">
          {looseItems.map((looseItem) => (
            <LooseItemCard
              key={looseItem.id}
              looseItem={looseItem}
              onOpen={() => {
                onOpenMedia(looseItem.media, () =>
                  model.refreshLooseItemAccess(looseItem.id),
                );
                model.seen.mutate(looseItem.publication_id);
              }}
            />
          ))}
        </div>
      ) : null}
    </section>
  );
}

function LooseItemCard({
  looseItem,
  onOpen,
}: {
  looseItem: LooseItem;
  onOpen: () => void;
}) {
  return (
    <article className="loose-item-card">
      <button
        aria-label={`Open Loose item ${looseItem.title || "Untitled"}`}
        onClick={onOpen}
        type="button"
      >
        <span
          className="event-cover"
          style={{
            aspectRatio:
              looseItem.media.width && looseItem.media.height
                ? `${looseItem.media.width} / ${looseItem.media.height}`
                : "1",
          }}
        >
          {looseItem.media.available ? (
            <img
              alt={mediaAlt(looseItem.media, 0)}
              loading="lazy"
              src={looseItem.media.thumbnail_url}
            />
          ) : (
            <span className="media-unavailable">Source unavailable</span>
          )}
        </span>
        <strong>{looseItem.title || "Untitled Loose item"}</strong>
        {looseItem.proposed_day ? <span>{looseItem.proposed_day}</span> : null}
        {looseItem.place_labels.length ? (
          <span>{looseItem.place_labels.join(", ")}</span>
        ) : null}
        <span>1 independently shared Media item</span>
      </button>
    </article>
  );
}
