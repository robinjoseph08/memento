import { useEffect, useState } from "react";

import {
  useExcludeEntries,
  useIncludeEntry,
  usePreviewExclude,
  usePreviewInclude,
} from "../../hooks/queries/albums";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { fieldErrors } from "../../lib/http";
import { mediaLabel } from "../../lib/media-labels";
import type {
  AlbumDetail,
  ExcludedEntry,
  Moment,
} from "../../types/generated/publishing";
import { Form, ReadFailure, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { VisibilityReview } from "./album-access";
import { AlbumImage } from "./album-image";
import { captureClock, countLabel, shortDay } from "./moment-labels";
import { SelectField } from "./structure-editor";

// Keep out takes selected media out of the Album while it stays in Immich.
// The review shows who loses it; the media waits in the Excluded section.
export function ExcludeDialog({
  album,
  moment,
  entryIDs,
  onClose,
  onSaved,
}: {
  album: AlbumDetail;
  moment: Moment;
  entryIDs: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const preview = usePreviewExclude(album.id, moment.id);
  const exclude = useExcludeEntries(album.id, moment.id);
  const review = preview.mutate;
  useEffect(() => {
    review({ entry_ids: entryIDs, review_token: "" });
  }, [review, entryIDs]);
  const returnFocus = useReturnFocus();
  const pending = exclude.isPending;
  const label = countLabel(entryIDs.length, "item", "items");
  return (
    <Dialog onOpenChange={(open) => !open && !pending && onClose()} open>
      <DialogContent onCloseAutoFocus={returnFocus}>
        <DialogTitle className="pr-8">
          Keep {label} out of this album?
        </DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          The media stays in Immich and moves to this album's Excluded section,
          where Add back returns it with its access decisions.
          {preview.data?.removes_moment &&
            ` ${moment.label} loses its last item and is removed.`}
          {!preview.data?.removes_moment &&
            entryIDs.includes(moment.cover_entry_id) &&
            " Its earliest remaining item becomes the cover."}
        </DialogDescription>
        <VisibilityReview
          album={album}
          changes={preview.data?.changes}
          className="my-6"
        />
        {preview.isPending && (
          <p className="mb-4 text-xs text-muted" role="status">
            Reviewing visibility…
          </p>
        )}
        {preview.isError && (
          <ReadFailure
            error={preview.error}
            pending={preview.isPending}
            retry={() => review({ entry_ids: entryIDs, review_token: "" })}
          />
        )}
        <Form
          aria-busy={pending}
          aria-label="Keep out of this album"
          error={exclude.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!preview.data || pending) return;
            exclude.mutate(
              { entry_ids: entryIDs, review_token: preview.data.review_token },
              { onSuccess: onSaved },
            );
          }}
        >
          <fieldset className="flex gap-2" disabled={pending}>
            <Button disabled={!preview.data} type="submit">
              {pending ? "Saving…" : "Keep out"}
            </Button>
            <Button onClick={onClose} type="button" variant="outline">
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}

function IncludeDialog({
  album,
  entry,
  onClose,
}: {
  album: AlbumDetail;
  entry: ExcludedEntry;
  onClose: () => void;
}) {
  const preview = usePreviewInclude(album.id, entry.id);
  const include = useIncludeEntry(album.id, entry.id);
  const day = entry.captured_at.slice(0, 10);
  const newKey = `new:${day}`;
  const options = [
    ...album.moments.map((moment) => ({
      value: moment.id,
      label: moment.label,
    })),
    { value: newKey, label: `New Moment: ${shortDay(day)}` },
  ];
  // The same rule the synchronization review uses: a Moment that already
  // holds this day, then one whose span covers it, else a new Moment.
  const suggested =
    album.moments.find((moment) =>
      moment.entries.some((item) => item.captured_at.slice(0, 10) === day),
    )?.id ??
    album.moments.find((moment) => moment.date <= day && day <= moment.end_date)
      ?.id ??
    newKey;
  const [momentID, setMomentID] = useState(suggested);
  const review = preview.mutate;
  useEffect(() => {
    review({ moment_id: momentID, review_token: "" });
  }, [review, momentID]);
  const returnFocus = useReturnFocus();
  const label = mediaLabel(entry);
  const errors = fieldErrors(preview.error);
  const pending = include.isPending;
  return (
    <Dialog onOpenChange={(open) => !open && !pending && onClose()} open>
      <DialogContent onCloseAutoFocus={returnFocus}>
        <DialogTitle className="pr-8">
          Add {entry.kind === "VIDEO" ? label : "this photo"} back?
        </DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          It returns with its access decisions and history. Choose where it
          belongs.
        </DialogDescription>
        <Form
          aria-busy={pending}
          aria-label={`Add ${entry.kind === "VIDEO" ? label : "this photo"} back`}
          className="mt-5"
          error={include.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!preview.data || pending) return;
            include.mutate(
              { moment_id: momentID, review_token: preview.data.review_token },
              { onSuccess: onClose },
            );
          }}
        >
          <SelectField
            error={errors.moment_id}
            label="Moment"
            onChange={setMomentID}
            options={options}
            placeholder="Choose a Moment"
            value={momentID}
          />
          <VisibilityReview
            album={album}
            changes={preview.data?.changes}
            className="my-6"
          />
          {preview.isPending && (
            <p className="mb-4 text-xs text-muted" role="status">
              Reviewing visibility…
            </p>
          )}
          {preview.isError && !errors.moment_id && (
            <div className="mb-4">
              <ReadFailure
                error={preview.error}
                pending={preview.isPending}
                retry={() => review({ moment_id: momentID, review_token: "" })}
              />
            </div>
          )}
          <fieldset className="flex gap-2" disabled={pending}>
            <Button disabled={!preview.data || preview.isPending} type="submit">
              {pending ? "Adding…" : "Add back"}
            </Button>
            <Button onClick={onClose} type="button" variant="outline">
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}

// The Excluded section lists media the Curator keeps out of the Album. A
// check for changes never offers it again; Add back is the only way in.
export function ExcludedPane({ album }: { album: AlbumDetail }) {
  const [including, setIncluding] = useState<ExcludedEntry | null>(null);
  return (
    <section aria-labelledby="excluded-heading">
      <h2 className={sectionHeadingClass} id="excluded-heading">
        Excluded
      </h2>
      <p className="mt-1 text-xs text-muted">
        {album.excluded.length === 0
          ? "Nothing is kept out. Select media in a Moment and choose Keep out to exclude it while it stays in Immich."
          : `${countLabel(album.excluded.length, "item", "items")} stay in Immich but not in this album. Checking for changes never adds them back; it only keeps their details current.`}
      </p>
      {album.excluded.length > 0 && (
        <ul aria-label="Excluded media" className="mt-5 divide-y divide-border">
          {album.excluded.map((entry) => {
            const label = mediaLabel(entry);
            return (
              <li
                className="flex flex-wrap items-center gap-3 py-3"
                key={entry.id}
              >
                <AlbumImage
                  alt={label}
                  className="h-16 w-auto max-w-24 rounded-sm"
                  fallback={
                    entry.available ? "No preview" : "Unavailable in Immich"
                  }
                  src={entry.available ? entry.thumbnail_url : ""}
                />
                <div className="min-w-0 flex-1 text-sm">
                  <span className="block font-medium wrap-anywhere">
                    {label}
                  </span>
                  {entry.kind === "VIDEO" && (
                    <span className="block text-muted">
                      {shortDay(entry.captured_at.slice(0, 10))},{" "}
                      {captureClock(entry.captured_at)} · Video
                    </span>
                  )}
                </div>
                <Button
                  disabled={!entry.available}
                  onClick={() => setIncluding(entry)}
                  size="sm"
                  title={
                    entry.available
                      ? undefined
                      : "Immich cannot show this item. Check for changes first."
                  }
                  type="button"
                  variant="outline"
                >
                  Add back
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      {including && (
        <IncludeDialog
          album={album}
          entry={including}
          key={including.id}
          onClose={() => setIncluding(null)}
        />
      )}
    </section>
  );
}
