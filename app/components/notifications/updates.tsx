import { ChevronDown, PartyPopper, RefreshCw, Send } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import {
  useApproveUpdates,
  useDeliveries,
  useDismissUpdates,
  useUpdatePreview,
} from "../../hooks/queries/notifications";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { submitOnModEnter } from "../../lib/forms";
import { errorMessage, fieldErrors } from "../../lib/http";
import { cn } from "../../lib/utils";
import type {
  Approval,
  NotificationAlbum,
  PersonResult,
  Preview,
  PreviewPerson,
} from "../../types/generated/notifications";
import { MediaCounts } from "../albums/media-counts";
import { countLabel } from "../albums/moment-labels";
import { ConfirmAction } from "../forms/confirm-action";
import {
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { Textarea } from "../ui/textarea";
import { DeliveryStatus } from "./delivery-status";

export function UpdatesPage() {
  const preview = useUpdatePreview();
  return (
    <>
      <PageTitle title="Updates" />
      <h1 className={headingClass}>Send updates</h1>
      <p className="mt-5 max-w-[640px] text-muted">
        Everyone below can now see photos, videos, or albums they have not been
        told about. Review what each person would hear about, leave anyone out,
        add a note, and send. Each person gets an update in Memento, and an
        email too when they asked for one. For changes that do not need an
        announcement, select them and choose Dismiss instead.
      </p>
      {preview.isPending && (
        <p className="mt-9" role="status">
          Checking for new updates…
        </p>
      )}
      {preview.isError && !preview.data && (
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
          onReview={() => void preview.refetch()}
          preview={preview.data}
          refreshError={preview.isError ? preview.error : null}
          refreshing={preview.isFetching}
        />
      )}
    </>
  );
}

function deliveryLabel(person: PreviewPerson, emailConfigured: boolean) {
  if (!person.update_email) return "In app only, no email selected";
  if (!person.email_eligible)
    return `In app only, email updates off for ${person.update_email}`;
  if (!emailConfigured)
    return `In app only, email is not configured for this installation`;
  return `Email to ${person.update_email}`;
}

// Exclusions share one set: own Album changes keyed by Album, and Albums new
// to view keyed apart, since one Album can be in both sections.
function albumKey(personID: string, albumID: string) {
  return `${personID}:${albumID}`;
}

function offeredKey(personID: string, albumID: string) {
  return `${personID}:offered:${albumID}`;
}

// A notification lists the newest few Albums new to view (shownOfferedAlbums
// in pkg/notifications), so the preview starts with as many and shows the
// rest on request, where any of them can be left out.
const shownOfferedAlbums = 5;

// What a row would still announce after the Curator's exclusions.
function remaining(person: PreviewPerson, excludedAlbums: Set<string>) {
  const albums = person.albums.filter(
    (album) => !excludedAlbums.has(albumKey(person.person_id, album.id)),
  );
  const offered = person.offered_albums.filter(
    (album) => !excludedAlbums.has(offeredKey(person.person_id, album.id)),
  ).length;
  return { albums, offered };
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

// Edits survive "Check again": the note stays, and exclusions are keyed by
// Person and Album IDs so they still apply to rows the new preview repeats.
// Sending clears the note and selections. Dismissing keeps the note for later.
function PreviewForm({
  preview,
  onReview,
  refreshing,
  refreshError,
}: {
  preview: Preview;
  onReview: () => void;
  refreshing: boolean;
  refreshError: unknown;
}) {
  const approve = useApproveUpdates();
  const dismiss = useDismissUpdates();
  const pending = approve.isPending || dismiss.isPending;
  const [note, setNote] = useState("");
  const [excludedPeople, setExcludedPeople] = useState<Set<string>>(
    () => new Set(),
  );
  const [excludedAlbums, setExcludedAlbums] = useState<Set<string>>(
    () => new Set(),
  );
  const noteId = useId();
  const errors = fieldErrors(approve.error);
  const included = preview.people.filter((person) => {
    if (excludedPeople.has(person.person_id)) return false;
    const left = remaining(person, excludedAlbums);
    return left.albums.length > 0 || left.offered > 0;
  });
  const edited =
    note.trim() !== "" || excludedPeople.size > 0 || excludedAlbums.size > 0;
  useUnsavedChanges(
    !approve.isSuccess &&
      (dismiss.isSuccess ? note.trim() !== "" : edited || pending),
  );
  const outcome = dismiss.isSuccess ? dismiss.data : approve.data;
  if (outcome) {
    return (
      <ApprovalResult
        approval={outcome}
        dismissed={dismiss.isSuccess}
        onReview={() => {
          if (approve.isSuccess) setNote("");
          approve.reset();
          dismiss.reset();
          setExcludedPeople(new Set());
          setExcludedAlbums(new Set());
          onReview();
        }}
        refreshing={refreshing}
      />
    );
  }
  if (preview.people.length === 0) {
    return (
      <EmptyState
        action={
          <Button disabled={refreshing} onClick={onReview} variant="outline">
            <RefreshCw
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            {refreshing ? "Checking…" : "Check again"}
          </Button>
        }
        className="mt-9"
        icon={PartyPopper}
        title="Everyone is up to date"
      >
        Nothing new is waiting to be announced. Publish an album or share more
        photos, then come back here to send updates.
      </EmptyState>
    );
  }
  function toggleAlbum(key: string, include: boolean) {
    setExcludedAlbums((current) => {
      const next = new Set(current);
      if (include) next.delete(key);
      else next.add(key);
      return next;
    });
  }
  const selectedPeople = included.map((person) => ({
    person_id: person.person_id,
    review_token: person.review_token,
    excluded_album_ids: person.albums
      .filter((album) =>
        excludedAlbums.has(albumKey(person.person_id, album.id)),
      )
      .map((album) => album.id),
    excluded_offered_album_ids: person.offered_albums
      .filter((album) =>
        excludedAlbums.has(offeredKey(person.person_id, album.id)),
      )
      .map((album) => album.id),
  }));
  return (
    <Form
      aria-busy={pending}
      aria-label="Send updates"
      className="mt-9"
      error={approve.error}
      onSubmit={(event) => {
        // The confirmation form is portalled, but its submit still bubbles here.
        if (event.target !== event.currentTarget) return;
        event.preventDefault();
        if (pending || included.length === 0) return;
        approve.mutate({ note, people: selectedPeople });
      }}
    >
      {refreshError !== null && (
        <p className="my-4 text-sm text-destructive" role="alert">
          Could not check for new updates. This list may be out of date.{" "}
          {errorMessage(refreshError)}
        </p>
      )}
      <fieldset disabled={pending}>
        <legend className={sectionHeadingClass}>
          {countLabel(included.length, "person", "people")} to update
        </legend>
        <ul className="mt-5 divide-y divide-border border-y border-border">
          {preview.people.map((person) => (
            <PersonRow
              emailConfigured={preview.email_configured}
              excludedAlbums={excludedAlbums}
              included={!excludedPeople.has(person.person_id)}
              key={person.person_id}
              onToggle={(include) =>
                setExcludedPeople((current) => {
                  const next = new Set(current);
                  if (include) next.delete(person.person_id);
                  else next.add(person.person_id);
                  return next;
                })
              }
              onToggleAlbum={toggleAlbum}
              person={person}
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
          <FieldError error={errors.people} id={`${noteId}-people`} />
        </div>
        <div className="mt-6 flex flex-wrap items-center gap-3">
          <Button disabled={included.length === 0} type="submit">
            <Send aria-hidden="true" className="size-4" strokeWidth={1.5} />
            {approve.isPending
              ? "Sending…"
              : `Send updates to ${countLabel(included.length, "person", "people")}`}
          </Button>
          <ConfirmAction
            description={`Clear the selected updates for ${countLabel(included.length, "person", "people")} without sending an email or creating an update in Memento. Their access stays the same. Unchecked updates stay pending, and newly shared photos and videos can still appear in future updates.`}
            disabled={included.length === 0 || approve.isPending || refreshing}
            error={dismiss.error}
            label="Dismiss selected updates"
            onConfirm={() => dismiss.mutateAsync({ people: selectedPeople })}
            pending={dismiss.isPending}
          />
          <Button disabled={refreshing} onClick={onReview} variant="ghost">
            <RefreshCw
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            {refreshing ? "Checking…" : "Check again"}
          </Button>
        </div>
      </fieldset>
    </Form>
  );
}

function PersonRow({
  person,
  included,
  emailConfigured,
  excludedAlbums,
  onToggle,
  onToggleAlbum,
}: {
  person: PreviewPerson;
  included: boolean;
  emailConfigured: boolean;
  excludedAlbums: Set<string>;
  onToggle: (include: boolean) => void;
  onToggleAlbum: (key: string, include: boolean) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [allOfferedShown, setAllOfferedShown] = useState(false);
  const detailsId = useId();
  const { albums, offered } = remaining(person, excludedAlbums);
  const counts = albumCounts(albums);
  const active = included && (albums.length > 0 || offered > 0);
  const allOffered = person.offered_albums.length;
  const shownOffered = allOfferedShown
    ? person.offered_albums
    : person.offered_albums.slice(0, shownOfferedAlbums);
  const hiddenOffered = allOffered - shownOffered.length;
  return (
    <li className={cn("py-4", !active && "text-muted")} data-included={active}>
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        <label className="flex min-w-0 grow basis-56 cursor-pointer items-start gap-3">
          <input
            aria-label={`Include ${person.display_name}`}
            checked={included}
            className="mt-1 size-4 shrink-0 cursor-pointer accent-primary"
            onChange={(event) => onToggle(event.target.checked)}
            type="checkbox"
          />
          <span className="min-w-0">
            <span className="block font-medium wrap-anywhere">
              {person.display_name}
            </span>
            <span className="block text-xs wrap-anywhere text-muted">
              {deliveryLabel(person, emailConfigured)}
            </span>
          </span>
        </label>
        <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
          {person.albums.length > 0 && (
            <>
              <span>
                {countLabel(albums.length, "album", "albums")}
                {albums.length < person.albums.length &&
                  ` (${person.albums.length - albums.length} left out)`}
              </span>
              <MediaCounts
                className="flex"
                photos={counts.photos}
                videos={counts.videos}
              />
            </>
          )}
          {allOffered > 0 && (
            <span>
              {countLabel(offered, "new album", "new albums")} to view
              {offered < allOffered && ` (${allOffered - offered} left out)`}
            </span>
          )}
          <Button
            aria-controls={detailsId}
            aria-expanded={expanded}
            aria-label={`${expanded ? "Hide" : "Show"} albums for ${person.display_name}`}
            className="-my-1 h-auto min-h-0 px-2 py-1 text-xs"
            onClick={() => setExpanded((value) => !value)}
            size="sm"
            variant="ghost"
          >
            {expanded ? "Hide" : "Show"}
            <ChevronDown
              aria-hidden="true"
              className={cn("size-3.5", expanded && "rotate-180")}
            />
          </Button>
        </span>
      </div>
      {expanded && (
        <div className="mt-3 ml-7 flex flex-col gap-4" id={detailsId}>
          {person.albums.length > 0 && (
            <ul className="flex flex-col gap-2">
              {person.albums.map((album) => (
                <AlbumChoice
                  album={album}
                  detail={album.status === "new" ? "New album" : "Updated"}
                  disabled={!included}
                  excludedAlbums={excludedAlbums}
                  exclusionKey={albumKey(person.person_id, album.id)}
                  key={album.id}
                  onToggle={onToggleAlbum}
                  personName={person.display_name}
                />
              ))}
            </ul>
          )}
          {allOffered > 0 && (
            <section aria-label={`New albums ${person.display_name} can view`}>
              <h3 className="text-xs font-medium">New albums you can view</h3>
              <ul className="mt-2 flex flex-col gap-2">
                {shownOffered.map((album) => (
                  <AlbumChoice
                    album={album}
                    detail="New to view"
                    disabled={!included}
                    excludedAlbums={excludedAlbums}
                    exclusionKey={offeredKey(person.person_id, album.id)}
                    key={album.id}
                    onToggle={onToggleAlbum}
                    personName={person.display_name}
                  />
                ))}
              </ul>
              {allOffered > shownOfferedAlbums && (
                <Button
                  aria-expanded={allOfferedShown}
                  className="mt-1 ml-5 h-auto min-h-0 px-2 py-1 text-sm"
                  onClick={() => setAllOfferedShown((value) => !value)}
                  size="sm"
                  variant="ghost"
                >
                  {allOfferedShown
                    ? "Show fewer albums"
                    : `Show ${countLabel(hiddenOffered, "more album", "more albums")}`}
                </Button>
              )}
            </section>
          )}
        </div>
      )}
    </li>
  );
}

function AlbumChoice({
  album,
  detail,
  disabled,
  excludedAlbums,
  exclusionKey,
  personName,
  onToggle,
}: {
  album: NotificationAlbum;
  detail: string;
  disabled: boolean;
  excludedAlbums: Set<string>;
  exclusionKey: string;
  personName: string;
  onToggle: (key: string, include: boolean) => void;
}) {
  const albumIncluded = !excludedAlbums.has(exclusionKey);
  return (
    <li
      className={cn(
        "flex flex-wrap items-start gap-x-4 gap-y-1 text-sm",
        !albumIncluded && "text-muted line-through",
      )}
    >
      <label className="flex min-w-0 cursor-pointer items-start gap-3">
        <input
          aria-label={`Include ${album.title} for ${personName}`}
          checked={albumIncluded}
          className="mt-1 size-4 shrink-0 cursor-pointer accent-primary"
          disabled={disabled}
          onChange={(event) => onToggle(exclusionKey, event.target.checked)}
          type="checkbox"
        />
        <span className="min-w-0">
          <span className="wrap-anywhere">{album.title}</span>
          <span className="text-muted">
            {" · "}
            {detail}
            {" · "}
            {countLabel(album.photo_count, "photo", "photos")},{" "}
            {countLabel(album.video_count, "video", "videos")}
          </span>
          {!albumIncluded && <span className="block text-xs">Left out</span>}
        </span>
      </label>
    </li>
  );
}

function ApprovalResult({
  approval,
  dismissed,
  onReview,
  refreshing,
}: {
  approval: Approval;
  dismissed: boolean;
  onReview: () => void;
  refreshing: boolean;
}) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  const status = dismissed ? "dismissed" : "notified";
  const sent = approval.people.filter((person) => person.status === status);
  const skipped = approval.people.filter((person) => person.status !== status);
  const emailed = sent.filter((person) => person.delivery);
  // Email settles in the background; watch it here until every message is
  // sent, failed, or needs a decision.
  const deliveries = useDeliveries(
    emailed.map((person) => person.delivery?.id ?? ""),
  );
  return (
    <section aria-labelledby="updates-result" className="mt-9">
      <h2
        className={sectionHeadingClass}
        id="updates-result"
        ref={headingRef}
        tabIndex={-1}
      >
        {dismissed
          ? sent.length === 0
            ? "No updates were dismissed"
            : `Updates dismissed for ${countLabel(sent.length, "person", "people")}`
          : sent.length === 0
            ? "No updates were sent"
            : `Updates sent to ${countLabel(sent.length, "person", "people")}`}
      </h2>
      <p className="mt-3 max-w-[640px] text-sm text-muted" role="status">
        {dismissed
          ? "No notifications or emails were sent. Access is unchanged. Check again for any remaining updates."
          : sent.length === 0
            ? "Nothing new was announced. Check again for the current updates."
            : emailed.length === 0
              ? "Each person now has an update in Memento."
              : `Each person now has an update in Memento. ${countLabel(emailed.length, "email is", "emails are")} on the way; failed or uncertain email also shows on your home page.`}
      </p>
      <ul className="mt-5 divide-y divide-border border-y border-border">
        {sent.map((person) => (
          <SentRow
            delivery={
              person.delivery
                ? (deliveries.data?.[person.delivery.id] ?? person.delivery)
                : null
            }
            key={person.person_id}
            person={person}
          />
        ))}
        {skipped.map((person) => (
          <li className="py-3 text-sm" key={person.person_id}>
            <span className="font-medium">
              {person.display_name || "Someone"}
            </span>
            <span className="text-muted">
              {dismissed ? " · Not dismissed. " : " · Not sent. "}
              {person.message}
            </span>
          </li>
        ))}
      </ul>
      <Button
        className="mt-6"
        disabled={refreshing}
        onClick={onReview}
        variant="outline"
      >
        <RefreshCw aria-hidden="true" className="size-4" strokeWidth={1.5} />
        {refreshing ? "Checking…" : "Check again"}
      </Button>
    </section>
  );
}

function SentRow({
  person,
  delivery,
}: {
  person: PersonResult;
  delivery: NonNullable<PersonResult["delivery"]> | null;
}) {
  return (
    <li className="py-3 text-sm">
      <span className="font-medium">{person.display_name}</span>
      <span className="text-muted">
        {" · "}
        {person.album_count > 0 && (
          <>
            {countLabel(person.album_count, "album", "albums")},{" "}
            {countLabel(person.photo_count, "photo", "photos")},{" "}
            {countLabel(person.video_count, "video", "videos")}
          </>
        )}
        {person.album_count > 0 && person.offered_album_count > 0 && ", "}
        {person.offered_album_count > 0 &&
          `${countLabel(person.offered_album_count, "new album", "new albums")} to view`}
      </span>
      {delivery ? (
        <DeliveryStatus
          className="mt-1"
          delivery={delivery}
          recipient={person.email}
        />
      ) : (
        <p className="mt-1 text-xs text-muted">
          {person.status === "dismissed"
            ? "No notification sent"
            : "In app only"}
        </p>
      )}
    </li>
  );
}
