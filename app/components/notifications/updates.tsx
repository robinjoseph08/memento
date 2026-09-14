import { ChevronDown } from "lucide-react";
import { useId, useState } from "react";

import {
  useApproveUpdates,
  useUpdatePreview,
} from "../../hooks/queries/notifications";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { submitOnModEnter } from "../../lib/forms";
import { fieldErrors } from "../../lib/http";
import { cn } from "../../lib/utils";
import type {
  Approval,
  NotificationAlbum,
  Preview,
  PreviewRecipient,
} from "../../types/generated/notifications";
import { MediaCounts } from "../albums/media-counts";
import { countLabel } from "../albums/moment-labels";
import {
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { Textarea } from "../ui/textarea";

export function UpdatesPage() {
  const preview = useUpdatePreview();
  return (
    <>
      <PageTitle title="Updates" />
      <h1 className={headingClass}>Send updates</h1>
      <p className="mt-5 max-w-[640px] text-muted">
        Everyone below can now see photos or videos they have not been told
        about. Review what each person would hear about, leave anyone out, add a
        note, and send. Each person gets an update in Memento; email is noted
        here for later.
      </p>
      {preview.isPending && (
        <p className="mt-9" role="status">
          Checking for new updates…
        </p>
      )}
      {preview.isError && (
        <div className="mt-9">
          <ReadFailure
            error={preview.error}
            pending={preview.isFetching}
            retry={preview.refetch}
          />
        </div>
      )}
      {preview.data && (
        <PreviewForm
          key={preview.dataUpdatedAt}
          onReview={() => void preview.refetch()}
          preview={preview.data}
          refreshing={preview.isFetching}
        />
      )}
    </>
  );
}

function deliveryLabel(recipient: PreviewRecipient) {
  if (recipient.email_eligible) return `Email to ${recipient.update_email}`;
  if (!recipient.update_email) return "In app only, no email selected";
  return `In app only, email updates off for ${recipient.update_email}`;
}

function albumCounts(albums: NotificationAlbum[]) {
  return albums.reduce(
    (counts, album) => ({
      photos: counts.photos + album.photo_count,
      videos: counts.videos + album.video_count,
    }),
    { photos: 0, videos: 0 },
  );
}

function PreviewForm({
  preview,
  onReview,
  refreshing,
}: {
  preview: Preview;
  onReview: () => void;
  refreshing: boolean;
}) {
  const approve = useApproveUpdates();
  const [note, setNote] = useState("");
  const [excludedPeople, setExcludedPeople] = useState<Set<string>>(
    () => new Set(),
  );
  const [excludedAlbums, setExcludedAlbums] = useState<Set<string>>(
    () => new Set(),
  );
  const noteId = useId();
  const errors = fieldErrors(approve.error);
  const included = preview.recipients.filter(
    (recipient) =>
      !excludedPeople.has(recipient.person_id) &&
      recipient.albums.some(
        (album) => !excludedAlbums.has(`${recipient.person_id}:${album.id}`),
      ),
  );
  const edited =
    note.trim() !== "" || excludedPeople.size > 0 || excludedAlbums.size > 0;
  useUnsavedChanges((edited || approve.isPending) && !approve.isSuccess);
  if (approve.isSuccess) {
    return (
      <ApprovalResult
        approval={approve.data}
        onReview={onReview}
        refreshing={refreshing}
      />
    );
  }
  if (preview.recipients.length === 0) {
    return (
      <section className="mt-9 border-t border-border py-8">
        <h2 className={sectionHeadingClass}>Everyone is up to date</h2>
        <p className="mt-3 max-w-120 text-muted">
          Nothing new is waiting to be announced. Publish an album or share more
          photos, then come back here to send updates.
        </p>
        <Button
          className="mt-5"
          disabled={refreshing}
          onClick={onReview}
          variant="outline"
        >
          {refreshing ? "Checking…" : "Check again"}
        </Button>
      </section>
    );
  }
  function toggleAlbum(personID: string, albumID: string, include: boolean) {
    setExcludedAlbums((current) => {
      const next = new Set(current);
      const key = `${personID}:${albumID}`;
      if (include) next.delete(key);
      else next.add(key);
      return next;
    });
  }
  return (
    <Form
      aria-busy={approve.isPending}
      aria-label="Send updates"
      className="mt-9"
      error={approve.error}
      onSubmit={(event) => {
        event.preventDefault();
        if (approve.isPending || included.length === 0) return;
        approve.mutate({
          note,
          recipients: included.map((recipient) => ({
            person_id: recipient.person_id,
            review_token: recipient.review_token,
            excluded_album_ids: recipient.albums
              .filter((album) =>
                excludedAlbums.has(`${recipient.person_id}:${album.id}`),
              )
              .map((album) => album.id),
          })),
        });
      }}
    >
      <fieldset disabled={approve.isPending}>
        <legend className={sectionHeadingClass}>
          {countLabel(included.length, "person", "people")} to update
        </legend>
        <ul className="mt-5 divide-y divide-border border-y border-border">
          {preview.recipients.map((recipient) => (
            <RecipientRow
              excludedAlbums={excludedAlbums}
              included={!excludedPeople.has(recipient.person_id)}
              key={recipient.person_id}
              onToggle={(include) =>
                setExcludedPeople((current) => {
                  const next = new Set(current);
                  if (include) next.delete(recipient.person_id);
                  else next.add(recipient.person_id);
                  return next;
                })
              }
              onToggleAlbum={(albumID, include) =>
                toggleAlbum(recipient.person_id, albumID, include)
              }
              recipient={recipient}
            />
          ))}
        </ul>
        <div className="mt-8 max-w-[640px]">
          <label className="mb-2 block text-xs font-medium" htmlFor={noteId}>
            Note for everyone (optional)
          </label>
          <Textarea
            aria-describedby={errors.note ? `${noteId}-error` : undefined}
            aria-invalid={!!errors.note}
            id={noteId}
            maxLength={1000}
            name="note"
            onChange={(event) => {
              approve.reset();
              setNote(event.target.value);
            }}
            onKeyDown={submitOnModEnter}
            placeholder="Finally got these together. Enjoy!"
            rows={3}
            value={note}
          />
          <FieldError error={errors.note} id={`${noteId}-error`} />
          <FieldError error={errors.recipients} id={`${noteId}-recipients`} />
        </div>
        <div className="mt-6 flex flex-wrap items-center gap-3">
          <Button disabled={included.length === 0} type="submit">
            {approve.isPending
              ? "Sending…"
              : `Send updates to ${countLabel(included.length, "person", "people")}`}
          </Button>
          <Button disabled={refreshing} onClick={onReview} variant="ghost">
            {refreshing ? "Checking…" : "Check again"}
          </Button>
        </div>
      </fieldset>
    </Form>
  );
}

function RecipientRow({
  recipient,
  included,
  excludedAlbums,
  onToggle,
  onToggleAlbum,
}: {
  recipient: PreviewRecipient;
  included: boolean;
  excludedAlbums: Set<string>;
  onToggle: (include: boolean) => void;
  onToggleAlbum: (albumID: string, include: boolean) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const detailsId = useId();
  const albums = recipient.albums.filter(
    (album) => !excludedAlbums.has(`${recipient.person_id}:${album.id}`),
  );
  const counts = albumCounts(albums);
  const active = included && albums.length > 0;
  return (
    <li className={cn("py-4", !active && "text-muted")} data-included={active}>
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        <label className="flex min-w-0 flex-1 cursor-pointer items-start gap-3">
          <input
            aria-label={`Include ${recipient.display_name}`}
            checked={included}
            className="mt-1 size-4 shrink-0 cursor-pointer accent-primary"
            onChange={(event) => onToggle(event.target.checked)}
            type="checkbox"
          />
          <span className="min-w-0">
            <span className="block font-medium wrap-anywhere">
              {recipient.display_name}
            </span>
            <span className="block text-xs wrap-anywhere text-muted">
              {deliveryLabel(recipient)}
            </span>
          </span>
        </label>
        <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
          <span>
            {countLabel(albums.length, "album", "albums")}
            {albums.length < recipient.albums.length &&
              ` (${recipient.albums.length - albums.length} left out)`}
          </span>
          <MediaCounts
            className="flex"
            photos={counts.photos}
            videos={counts.videos}
          />
          <Button
            aria-controls={detailsId}
            aria-expanded={expanded}
            aria-label={`${expanded ? "Hide" : "Show"} albums for ${recipient.display_name}`}
            className="-my-1 h-auto min-h-0 px-2 py-1 text-xs"
            onClick={() => setExpanded((value) => !value)}
            size="sm"
            variant="ghost"
          >
            {countLabel(recipient.albums.length, "album", "albums")}
            <ChevronDown
              aria-hidden="true"
              className={cn("size-3.5", expanded && "rotate-180")}
            />
          </Button>
        </span>
      </div>
      {expanded && (
        <ul className="mt-3 ml-7 flex flex-col gap-2" id={detailsId}>
          {recipient.albums.map((album) => {
            const albumIncluded = !excludedAlbums.has(
              `${recipient.person_id}:${album.id}`,
            );
            return (
              <li
                className={cn(
                  "flex flex-wrap items-start gap-x-4 gap-y-1 text-sm",
                  !albumIncluded && "text-muted line-through",
                )}
                key={album.id}
              >
                <label className="flex min-w-0 cursor-pointer items-start gap-3">
                  <input
                    aria-label={`Include ${album.title} for ${recipient.display_name}`}
                    checked={albumIncluded}
                    className="mt-1 size-4 shrink-0 cursor-pointer accent-primary"
                    disabled={!included}
                    onChange={(event) =>
                      onToggleAlbum(album.id, event.target.checked)
                    }
                    type="checkbox"
                  />
                  <span className="min-w-0">
                    <span className="wrap-anywhere">{album.title}</span>
                    <span className="text-muted">
                      {" · "}
                      {album.status === "new" ? "New album" : "Updated"}
                      {" · "}
                      {countLabel(album.photo_count, "photo", "photos")},{" "}
                      {countLabel(album.video_count, "video", "videos")}
                    </span>
                    {album.video_titles.length > 0 && (
                      <span className="block text-xs wrap-anywhere text-muted">
                        Videos: {album.video_titles.join(", ")}
                      </span>
                    )}
                    {!albumIncluded && (
                      <span className="block text-xs">Left out</span>
                    )}
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}

function ApprovalResult({
  approval,
  onReview,
  refreshing,
}: {
  approval: Approval;
  onReview: () => void;
  refreshing: boolean;
}) {
  const sent = approval.recipients.filter(
    (recipient) => recipient.status === "notified",
  );
  const skipped = approval.recipients.filter(
    (recipient) => recipient.status !== "notified",
  );
  return (
    <section aria-labelledby="updates-result" className="mt-9">
      <h2 className={sectionHeadingClass} id="updates-result">
        {sent.length === 0
          ? "No updates were sent"
          : `Updates sent to ${countLabel(sent.length, "person", "people")}`}
      </h2>
      <p className="mt-3 max-w-[640px] text-sm text-muted" role="status">
        {sent.length === 0
          ? "Nothing new was announced. Check again for the current updates."
          : "Each person now has an update in Memento."}
      </p>
      <ul className="mt-5 divide-y divide-border border-y border-border">
        {sent.map((recipient) => (
          <li className="py-3 text-sm" key={recipient.person_id}>
            <span className="font-medium">{recipient.display_name}</span>
            <span className="text-muted">
              {" · "}
              {countLabel(recipient.album_count, "album", "albums")},{" "}
              {countLabel(recipient.photo_count, "photo", "photos")},{" "}
              {countLabel(recipient.video_count, "video", "videos")}
            </span>
          </li>
        ))}
        {skipped.map((recipient) => (
          <li className="py-3 text-sm" key={recipient.person_id}>
            <span className="font-medium">
              {recipient.display_name || "Someone"}
            </span>
            <span className="text-muted"> · Not sent. {recipient.message}</span>
          </li>
        ))}
      </ul>
      <Button
        className="mt-6"
        disabled={refreshing}
        onClick={onReview}
        variant="outline"
      >
        {refreshing ? "Checking…" : "Check again"}
      </Button>
    </section>
  );
}
