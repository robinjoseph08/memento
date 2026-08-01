import { formatDateRange } from "../format";
import type { Event, MediaItem, StagedChange } from "../types/generated/events";

const stagedLabels: Record<StagedChange["kind"], string> = {
  addition: "Additions",
  removal: "Removals",
  move: "Moves and ordering",
  metadata: "Metadata edits",
  moment_structure: "Moment structure",
  access: "Access changes",
};

function mediaLabel(item: Pick<MediaItem, "media_type" | "local_date_time">) {
  if (!item.local_date_time) return `Undated ${item.media_type}`;
  const parsed = new Date(item.local_date_time);
  return Number.isNaN(parsed.valueOf())
    ? `Undated ${item.media_type}`
    : `${item.media_type}, ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed)}`;
}

function countedNoun(count: number, singular: string, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`;
}

function stagedCountLabel(change: StagedChange) {
  switch (change.kind) {
    case "addition":
    case "removal":
    case "move":
      return countedNoun(change.count, "Media item");
    case "moment_structure":
      return countedNoun(change.count, "Moment");
    case "access":
      return change.recipient_access?.length
        ? countedNoun(change.count, "Recipient")
        : countedNoun(change.moment_ids.length, "Moment");
    case "metadata": {
      const parts = [
        change.event_metadata_fields?.length ? "1 Event" : undefined,
        change.moment_ids.length
          ? countedNoun(change.moment_ids.length, "Moment")
          : undefined,
        change.media_item_ids.length
          ? countedNoun(change.media_item_ids.length, "Media item")
          : undefined,
      ].filter((part): part is string => part !== undefined);
      if (parts.length === 1) return parts[0];
      if (parts.length === 2) return `${parts[0]} and ${parts[1]}`;
      if (parts.length > 2)
        return `${parts.slice(0, -1).join(", ")}, and ${parts.at(-1)}`;
      return countedNoun(change.count, "affected item");
    }
  }
}

export function StagedChangeLabels({
  kinds,
}: {
  kinds: StagedChange["kind"][];
}) {
  if (kinds.length === 0) return null;
  const labels = kinds.map((kind) => stagedLabels[kind]);
  return (
    <span
      aria-label={`Staged changes: ${labels.join(", ")}`}
      className="staged-change-label"
    >
      Staged: {labels.join(", ")}
    </span>
  );
}

export function StagedUpdateReview({
  event,
  onRestoreMedia,
  restoringMediaID,
  restoreDisabled,
  restoreError,
  restoreConflict,
  restoreRecoveryPending,
  onRecoverRestore,
  metadataValid,
}: {
  event: Event;
  onRestoreMedia: (mediaID: string) => void;
  restoringMediaID?: string;
  restoreDisabled: boolean;
  restoreError?: string;
  restoreConflict: boolean;
  restoreRecoveryPending: boolean;
  onRecoverRestore: () => void;
  metadataValid: boolean;
}) {
  if (!event.staged_update) return null;
  const removedMedia = event.staged_update.changes.flatMap(
    (change) => change.removed_media ?? [],
  );
  const deletedMoments = event.staged_update.changes.flatMap(
    (change) => change.deleted_moments ?? [],
  );
  const changedEventMetadata = new Set(
    event.staged_update.changes.flatMap(
      (change) => change.event_metadata_fields ?? [],
    ),
  );
  const selectedCover = event.moments
    .flatMap((moment) => moment.media_items)
    .find((item) => item.id === event.selected_cover_media_item_id);
  const metadataLabel = (
    field:
      | "title"
      | "description"
      | "date_start"
      | "date_end"
      | "selected_cover_media_item_id"
      | "place_labels"
      | "grouping_timezone",
  ) =>
    changedEventMetadata.has(field) ? (
      <span className="staged-change-label">Staged: Metadata edits</span>
    ) : null;
  return (
    <section aria-labelledby="staged-review-title" className="staged-review">
      <div>
        <p className="step-label">Private until Publication</p>
        <h4 id="staged-review-title">Staged update review</h4>
        <p>
          Review the Event details and organization that will replace the
          current Publication.
        </p>
        <section
          aria-labelledby="staged-event-metadata-title"
          className="staged-event-metadata"
        >
          <h5 id="staged-event-metadata-title">
            {metadataValid
              ? "Event details that will publish"
              : "Event details not ready to publish"}
          </h5>
          {!metadataValid ? (
            <p className="form-error" role="alert">
              Fix the Event detail validation errors before this review can be
              saved or published.
            </p>
          ) : null}
          <dl>
            <div
              className={
                changedEventMetadata.has("title") ? "staged-metadata" : ""
              }
            >
              <dt>Title {metadataLabel("title")}</dt>
              <dd>{event.title || "Untitled Event"}</dd>
            </div>
            <div
              className={
                changedEventMetadata.has("description") ? "staged-metadata" : ""
              }
            >
              <dt>Description {metadataLabel("description")}</dt>
              <dd>{event.description || "No description"}</dd>
            </div>
            <div
              className={
                changedEventMetadata.has("date_start") ||
                changedEventMetadata.has("date_end")
                  ? "staged-metadata"
                  : ""
              }
            >
              <dt>
                Date range{" "}
                {changedEventMetadata.has("date_start") ||
                changedEventMetadata.has("date_end") ? (
                  <span className="staged-change-label">
                    Staged: Metadata edits
                  </span>
                ) : null}
              </dt>
              <dd>{formatDateRange(event.date_start, event.date_end)}</dd>
            </div>
            <div
              className={
                changedEventMetadata.has("selected_cover_media_item_id")
                  ? "staged-metadata"
                  : ""
              }
            >
              <dt>
                Event cover {metadataLabel("selected_cover_media_item_id")}
              </dt>
              <dd>
                {selectedCover
                  ? mediaLabel(selectedCover)
                  : event.selected_cover_media_item_id
                    ? "Selected cover is not assigned"
                    : "Automatic safe cover selection"}
              </dd>
            </div>
            <div
              className={
                changedEventMetadata.has("place_labels")
                  ? "staged-metadata"
                  : ""
              }
            >
              <dt>Place labels {metadataLabel("place_labels")}</dt>
              <dd>
                {event.place_labels.length > 0
                  ? event.place_labels.join(", ")
                  : "No Place labels"}
              </dd>
            </div>
            <div
              className={
                changedEventMetadata.has("grouping_timezone")
                  ? "staged-metadata"
                  : ""
              }
            >
              <dt>Grouping timezone {metadataLabel("grouping_timezone")}</dt>
              <dd>{event.grouping_timezone}</dd>
            </div>
          </dl>
        </section>
        {removedMedia.length > 0 || deletedMoments.length > 0 ? (
          <section
            aria-labelledby="removed-items-title"
            className="staged-removed"
          >
            <h5 id="removed-items-title">Removed from the resulting Event</h5>
            <ul>
              {removedMedia.map((item) => (
                <li className="staged-removal" key={`media-${item.id}`}>
                  <strong>Removed Media</strong>
                  <span>{mediaLabel(item)}</span>
                  <code>{item.id}</code>
                  {item.restorable ? (
                    <button
                      disabled={
                        restoreDisabled || restoringMediaID !== undefined
                      }
                      onClick={() => onRestoreMedia(item.id)}
                      type="button"
                    >
                      {restoringMediaID === item.id
                        ? "Restoring…"
                        : "Restore removed Media"}
                    </button>
                  ) : (
                    <small>
                      Restore unavailable because the Source no longer contains
                      this Media.
                    </small>
                  )}
                </li>
              ))}
              {deletedMoments.map((moment) => (
                <li className="staged-removal" key={`moment-${moment.id}`}>
                  <strong>Deleted Moment</strong>
                  <span>{moment.title || moment.proposed_day}</span>
                  <code>{moment.id}</code>
                </li>
              ))}
            </ul>
            {restoreConflict ? (
              <div className="form-error">
                <p role="alert">
                  This Event changed in another browser. Load the newer Event
                  before retrying this restoration.
                </p>
                <button
                  disabled={restoreRecoveryPending}
                  onClick={onRecoverRestore}
                  type="button"
                >
                  {restoreRecoveryPending
                    ? "Loading newer Event…"
                    : "Load newer Event and retry restoration"}
                </button>
                {restoreError ? <p role="alert">{restoreError}</p> : null}
              </div>
            ) : restoreError ? (
              <p className="form-error" role="alert">
                {restoreError}
              </p>
            ) : null}
          </section>
        ) : null}
      </div>
      <ul aria-label="Net change summary">
        {event.staged_update.changes.map((change) => (
          <li key={change.kind}>
            <strong>{stagedLabels[change.kind]}</strong>
            <span>{stagedCountLabel(change)}</span>
            <small>{change.detail}</small>
            {change.kind === "access" && change.recipient_access?.length ? (
              <ul
                aria-label="Recipient access changes"
                className="recipient-access-changes"
              >
                {change.recipient_access.map((access) => (
                  <li key={access.recipient_person_id}>
                    <strong>{access.recipient_name}</strong>
                    <span>
                      {access.granted_media_count > 0
                        ? `${access.granted_media_count} Media granted`
                        : ""}
                      {access.granted_media_count > 0 &&
                      access.revoked_media_count > 0
                        ? ", "
                        : ""}
                      {access.revoked_media_count > 0
                        ? `${access.revoked_media_count} Media revoked`
                        : ""}
                    </span>
                  </li>
                ))}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
