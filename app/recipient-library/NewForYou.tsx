import type { EventSummary } from "../types/generated/library";
import { EventGallery } from "./MediaGallery";
import { LibraryError } from "./presentation";
import type { NewForYouModel } from "./useNewForYouModel";

export function NewForYou({
  model,
  onOpenEvent,
}: {
  model: NewForYouModel;
  onOpenEvent: (event: EventSummary) => void;
}) {
  return (
    <>
      <LibraryError error={model.newForYou.error ?? model.seen.error} />
      {model.newForYou.data?.events.length ? (
        <section aria-labelledby="new-for-you-title" className="new-for-you">
          <h2 id="new-for-you-title">New for you</h2>
          <EventGallery
            events={model.newForYou.data.events}
            onOpen={(event) => {
              onOpenEvent(event);
              model.seen.mutate(event.publication_id);
            }}
          />
        </section>
      ) : null}
    </>
  );
}
