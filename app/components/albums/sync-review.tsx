import { useEffect, useId, useState } from "react";

import { useApplySync, useCheckSync } from "../../hooks/queries/albums";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { fieldErrors } from "../../lib/http";
import { disambiguatePhotoLabels, mediaLabel } from "../../lib/media-labels";
import type {
  AlbumDetail,
  SyncAddition,
  SyncChange,
  SyncCoverChoice,
  SyncRemoval,
  SyncRequest,
  SyncReview,
} from "../../types/generated/publishing";
import { Failure, FieldError, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { VisibilityReview } from "./album-access";
import { AlbumImage } from "./album-image";
import { captureClock, countLabel } from "./moment-labels";

// The Combobox value that keeps a source asset out of this Album.
const EXCLUDE = "exclude";

type Placements = Record<string, { moment_id: string; exclude: boolean }>;
type Covers = Record<string, string>;

function syncRequest(placements: Placements, covers: Covers): SyncRequest {
  return {
    placements: Object.entries(placements).map(([source_id, choice]) => ({
      source_id,
      ...choice,
    })),
    covers: Object.entries(covers).map(([moment_id, entry_id]) => ({
      moment_id,
      entry_id,
    })),
    review_token: "",
  };
}

function captureDay(value: string) {
  const date = new Date(`${value.slice(0, 10)}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return value.slice(0, 10);
  return date.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

function captureLabel(value: string) {
  return `${captureDay(value)}, ${captureClock(value)}`;
}

function alsoIn(albums: { id: string; title: string }[], verb: string) {
  if (albums.length === 0) return null;
  return (
    <span className="block text-muted">
      {verb} {albums.map((album) => album.title).join(", ")}
    </span>
  );
}

function changeSummary(change: SyncChange) {
  const parts: string[] = [];
  for (const field of change.fields) {
    if (field === "checksum") parts.push("new file version");
    else if (field === "capture_time")
      parts.push(
        `capture time ${captureLabel(change.captured_at)} becomes ${captureLabel(change.new_captured_at)}`,
      );
    else if (field === "availability")
      parts.push(
        change.new_available ? "available again" : "unavailable in Immich",
      );
    else if (field === "filename") parts.push("renamed");
    else parts.push("updated details");
  }
  return parts.join(", ");
}

function Thumbnail({ src, alt }: { src: string; alt: string }) {
  return (
    <AlbumImage
      alt={alt}
      className="h-16 w-auto max-w-24 rounded-sm"
      fallback="No preview"
      src={src}
    />
  );
}

function AdditionRow({
  actionLabel,
  addition,
  label,
  review,
  value,
  onChange,
}: {
  actionLabel: string;
  addition: SyncAddition;
  label: string;
  review: SyncReview;
  value: string;
  onChange: (value: string) => void;
}) {
  const id = useId();
  const options = [
    ...review.moments.map((moment) => ({
      value: moment.id,
      label: moment.new ? `New Moment: ${moment.label}` : moment.label,
      description:
        moment.id === addition.suggested_moment_id ? "Suggested" : undefined,
    })),
    { value: EXCLUDE, label: "Keep out of this album" },
  ];
  return (
    <li className="flex flex-wrap items-start gap-3 py-3">
      <Thumbnail alt={label} src={addition.thumbnail_url} />
      <div className="min-w-0 flex-1 text-sm">
        <span className="block font-medium wrap-anywhere">{label}</span>
        {(addition.kind === "VIDEO" || addition.returning) && (
          <span className="block text-muted">
            {addition.kind === "VIDEO" && (
              <>{captureLabel(addition.captured_at)} · Video</>
            )}
            {addition.kind === "VIDEO" && addition.returning && " · "}
            {addition.returning && "Previously in this album"}
          </span>
        )}
        {alsoIn(addition.other_albums, "Also in")}
      </div>
      <div className="w-full min-[560px]:w-64">
        <label className="sr-only" id={`${id}-label`}>
          Destination for {actionLabel}
        </label>
        <Combobox
          aria-labelledby={`${id}-label`}
          onChange={onChange}
          options={options}
          placeholder="Choose a Moment"
          value={value}
        />
      </div>
    </li>
  );
}

function RemovalRow({ removal }: { removal: SyncRemoval }) {
  const label = mediaLabel(removal);
  return (
    <li className="flex items-start gap-3 py-3">
      <Thumbnail alt={label} src={removal.thumbnail_url} />
      <div className="min-w-0 flex-1 text-sm">
        <span className="block font-medium wrap-anywhere">{label}</span>
        <span className="block text-muted">
          {removal.reason === "deleted"
            ? "Deleted from Immich"
            : removal.reason === "trashed"
              ? "In the Immich trash"
              : "Removed from the Immich album"}{" "}
          · leaves {removal.excluded ? "Excluded media" : removal.moment_label}
          {removal.cover && " · was the cover"}
        </span>
      </div>
    </li>
  );
}

function CoverChoiceField({
  choice,
  value,
  onChange,
  error,
}: {
  choice: SyncCoverChoice;
  value: string;
  onChange: (value: string) => void;
  error?: string;
}) {
  const id = useId();
  const labels = choice.options.map(mediaLabel);
  const actionLabels = disambiguatePhotoLabels(labels);
  return (
    <fieldset
      aria-describedby={error ? `${id}-error` : undefined}
      aria-invalid={!!error}
      className="py-3"
    >
      <legend className="text-sm font-medium">
        Choose a new cover for {choice.moment_label}
      </legend>
      <div className="mt-2 flex flex-wrap gap-3">
        {choice.options.map((option, index) => {
          const label = labels[index];
          return (
            <label
              className="flex max-w-40 cursor-pointer flex-col gap-2 text-xs"
              key={option.entry_id}
            >
              <input
                aria-label={`Use ${actionLabels[index]} as the cover of ${choice.moment_label}`}
                checked={value === option.entry_id}
                className="peer sr-only"
                name={`cover-${choice.moment_id}`}
                onChange={() => onChange(option.entry_id)}
                type="radio"
                value={option.entry_id}
              />
              <span className="block rounded-sm peer-checked:outline-2 peer-checked:outline-offset-2 peer-checked:outline-primary peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-ring">
                <AlbumImage
                  alt={label}
                  className="h-auto max-h-28 w-auto max-w-40"
                  fallback="No preview available"
                  src={option.thumbnail_url}
                />
              </span>
              <span className="wrap-anywhere text-muted">{label}</span>
            </label>
          );
        })}
      </div>
      <FieldError error={error} id={`${id}-error`} />
    </fieldset>
  );
}

function AdditionSection({
  additions,
  label,
  note,
  review,
  value,
  onPlace,
  error,
}: {
  additions: SyncAddition[];
  label: string;
  note: string;
  review: SyncReview;
  value: (addition: SyncAddition) => string;
  onPlace: (sourceID: string, value: string) => void;
  error?: string;
}) {
  if (additions.length === 0) return null;
  const labels = additions.map(mediaLabel);
  const actionLabels = disambiguatePhotoLabels(labels);
  return (
    <section aria-label={label} className="border-t border-border py-4">
      <h3 className="font-heading text-xl">
        {label} ({additions.length})
      </h3>
      <p className="mt-1 text-xs text-muted">{note}</p>
      <FieldError error={error} id={`${label}-error`} />
      <ul className="mt-2 divide-y divide-border">
        {additions.map((addition, index) => (
          <AdditionRow
            actionLabel={actionLabels[index]}
            addition={addition}
            key={addition.source_id}
            label={labels[index]}
            onChange={(next) => onPlace(addition.source_id, next)}
            review={review}
            value={value(addition)}
          />
        ))}
      </ul>
    </section>
  );
}

// Check for changes compares the Album with its Immich album and shows the
// difference for review. Nothing changes until the Curator applies it, and
// closing the dialog discards the review.
export function SyncDialog({
  album,
  onClose,
}: {
  album: AlbumDetail;
  onClose: () => void;
}) {
  const check = useCheckSync(album.id);
  const apply = useApplySync(album.id);
  const returnFocus = useReturnFocus();
  const [placements, setPlacements] = useState<Placements>({});
  const [covers, setCovers] = useState<Covers>({});
  const [review, setReview] = useState<SyncReview | null>(null);
  const runCheck = check.mutate;
  useEffect(() => {
    runCheck(syncRequest({}, {}), { onSuccess: setReview });
  }, [runCheck]);
  const recheck = (nextPlacements: Placements, nextCovers: Covers) => {
    setPlacements(nextPlacements);
    setCovers(nextCovers);
    apply.reset();
    runCheck(syncRequest(nextPlacements, nextCovers), { onSuccess: setReview });
  };
  const place = (sourceID: string, value: string) =>
    recheck(
      {
        ...placements,
        [sourceID]:
          value === EXCLUDE
            ? { moment_id: "", exclude: true }
            : { moment_id: value, exclude: false },
      },
      covers,
    );
  const errors = fieldErrors(check.error);
  const pending = apply.isPending;
  const busy = check.isPending;
  // A failed recheck leaves the last review on screen but its token no
  // longer matches the current choices, so applying waits for a good check.
  const canApply =
    !!review && review.ready && !busy && !pending && !check.isError;
  const additions = review?.additions ?? [];
  const showUpToDate = !!review && review.up_to_date;
  return (
    <Dialog onOpenChange={(open) => !open && !pending && onClose()} open>
      <DialogContent className="max-w-2xl" onCloseAutoFocus={returnFocus}>
        <DialogTitle className="pr-8">Changes in Immich</DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Memento compares this album with its Immich album. Nothing changes
          until you apply the review, and closing discards it.
        </DialogDescription>
        {busy && (
          <p className="mt-5 text-sm" role="status">
            {review ? "Updating the review…" : "Checking Immich…"}
          </p>
        )}
        {check.isError && (
          <div className="mt-5">
            <Failure error={check.error} />
            <Button
              disabled={busy}
              onClick={() => recheck(placements, covers)}
              variant="outline"
            >
              Check again
            </Button>
          </div>
        )}
        {review && (
          <Form
            aria-busy={pending}
            aria-label="Apply changes from Immich"
            className="mt-5"
            error={apply.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (!canApply) return;
              apply.mutate(
                {
                  ...syncRequest(placements, covers),
                  review_token: review.review_token,
                },
                { onSuccess: onClose },
              );
            }}
          >
            {review.faces_message && (
              <p className="mb-4 text-xs text-muted" role="status">
                {review.faces_message}
              </p>
            )}
            {showUpToDate && (
              <p className="text-sm" role="status">
                This album matches Immich. Nothing to apply.
              </p>
            )}
            {review.description && (
              <section
                aria-label="Description change"
                className="border-t border-border py-4"
              >
                <h3 className="font-heading text-xl">Description</h3>
                <p className="mt-2 text-sm wrap-anywhere text-muted line-through">
                  {review.description.before || "No description"}
                </p>
                <p className="mt-1 text-sm wrap-anywhere">
                  {review.description.after || "No description"}
                </p>
              </section>
            )}
            <AdditionSection
              additions={additions}
              error={errors.placements}
              label="New in Immich"
              note="Each item joins the suggested Moment unless you choose another one. New Moments get the item's capture day."
              onPlace={place}
              review={review}
              value={(addition) =>
                addition.exclude ? EXCLUDE : addition.moment_id
              }
            />
            {review.removals.length > 0 && (
              <section
                aria-label="Removed in Immich"
                className="border-t border-border py-4"
              >
                <h3 className="font-heading text-xl">
                  Removed in Immich ({review.removals.length})
                </h3>
                <p className="mt-1 text-xs text-muted">
                  These leave the album. Access decisions and notification
                  history are kept in case they return. To keep something out
                  that is still in Immich, use Keep out from its Moment.
                </p>
                <ul className="mt-2 divide-y divide-border">
                  {review.removals.map((removal) => (
                    <RemovalRow key={removal.entry_id} removal={removal} />
                  ))}
                </ul>
                {review.removed_moments.length > 0 && (
                  <p className="mt-3 text-sm" role="note">
                    {review.removed_moments.length === 1
                      ? `${review.removed_moments[0]?.label} loses its last item and is removed.`
                      : `${countLabel(review.removed_moments.length, "Moment", "Moments")} lose their last item and are removed: ${review.removed_moments.map((moment) => moment.label).join(", ")}.`}
                  </p>
                )}
                {review.cover_choices.map((choice) => (
                  <CoverChoiceField
                    choice={choice}
                    error={errors.covers}
                    key={choice.moment_id}
                    onChange={(value) =>
                      recheck(placements, {
                        ...covers,
                        [choice.moment_id]: value,
                      })
                    }
                    value={choice.entry_id}
                  />
                ))}
              </section>
            )}
            {review.changes.length > 0 && (
              <section
                aria-label="Changed in Immich"
                className="border-t border-border py-4"
              >
                <h3 className="font-heading text-xl">
                  Changed in Immich ({review.changes.length})
                </h3>
                <ul className="mt-2 divide-y divide-border">
                  {review.changes.map((change) => (
                    <li
                      className="flex items-start gap-3 py-3"
                      key={change.entry_id}
                    >
                      <Thumbnail
                        alt={mediaLabel(change)}
                        src={change.thumbnail_url}
                      />
                      <div className="min-w-0 flex-1 text-sm">
                        <span className="block font-medium wrap-anywhere">
                          {mediaLabel(change)}
                        </span>
                        <span className="block text-muted">
                          {changeSummary(change)}
                          {change.excluded && " · in Excluded media"}
                        </span>
                        {alsoIn(change.other_albums, "Also updates in")}
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {!showUpToDate && (
              <VisibilityReview
                album={album}
                changes={review.audience}
                className="border-b-0"
              />
            )}
            {review.blockers.length > 0 && (
              <ul className="mt-2 mb-2 text-sm text-destructive" role="alert">
                {review.blockers.map((blocker) => (
                  <li key={blocker}>{blocker}</li>
                ))}
              </ul>
            )}
            <fieldset
              className={
                showUpToDate
                  ? "mt-6 flex flex-wrap gap-2"
                  : "mt-2 flex flex-wrap gap-2 border-t border-border pt-5"
              }
              disabled={pending}
            >
              {!showUpToDate && (
                <Button disabled={!canApply} type="submit">
                  {pending ? "Applying…" : "Apply changes"}
                </Button>
              )}
              <Button onClick={onClose} type="button" variant="outline">
                {showUpToDate ? "Close" : "Cancel"}
              </Button>
              {apply.isError && (
                <Button
                  onClick={() => recheck(placements, covers)}
                  type="button"
                  variant="ghost"
                >
                  Check again
                </Button>
              )}
            </fieldset>
          </Form>
        )}
      </DialogContent>
    </Dialog>
  );
}
