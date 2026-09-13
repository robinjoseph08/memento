import { useEffect, useEffectEvent, useState } from "react";

import { useRetryChapters, useUpdateVideo } from "../../hooks/queries/albums";
import { fieldErrors } from "../../lib/http";
import type { Entry } from "../../types/generated/publishing";
import { Failure, Field, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import { clock } from "../viewer/labels";
import { filenameTitle } from "./moment-labels";

// One video's Memento title and its chapter state, shown inside the item
// dialog above the access rules. The title is global to the Media Item, so
// every Album showing the video changes with it. Chapters are read-only facts
// from the file; a failed extraction can be retried here and never blocks
// playback or publication. onDirty tells the dialog about unsaved edits,
// onEdit fires on every keystroke, and onSaved fires once a saved title is
// reflected in the entry.
export function VideoDetails({
  albumID,
  entry,
  onDirty,
  onEdit,
  onSaved,
}: {
  albumID: string;
  entry: Entry;
  onDirty: (dirty: boolean) => void;
  onEdit: () => void;
  onSaved: () => void;
}) {
  const update = useUpdateVideo(albumID, entry.id);
  const retry = useRetryChapters(albumID, entry.id);
  const [title, setTitle] = useState(entry.title);
  const dirty = title.trim() !== entry.title;
  const reportDirty = useEffectEvent(onDirty);
  useEffect(() => {
    reportDirty(dirty || update.isPending);
  }, [dirty, update.isPending]);
  // A save reports success one render before the refreshed Album reaches this
  // entry, so wait for the entry to carry the saved title.
  const saved = useEffectEvent(onSaved);
  useEffect(() => {
    if (update.isSuccess && !dirty) saved();
  }, [update.isSuccess, dirty]);
  const errors = fieldErrors(update.error);
  return (
    <>
      <Form
        aria-busy={update.isPending}
        aria-label="Video title"
        className="mt-6"
        error={update.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (update.isPending || !dirty) return;
          update.mutate(
            { title: title.trim() },
            {
              // Keep the field on the stored value so a server-side
              // normalization cannot leave the form looking unsaved.
              onSuccess: (album) =>
                setTitle(
                  album.moments
                    .flatMap((moment) => moment.entries)
                    .find((item) => item.id === entry.id)?.title ?? "",
                ),
            },
          );
        }}
      >
        <fieldset disabled={update.isPending}>
          <Field
            error={errors.title}
            label="Video title"
            maxLength={200}
            name="title"
            onChange={(event) => {
              update.reset();
              onEdit();
              setTitle(event.target.value);
            }}
            placeholder={filenameTitle(entry.filename)}
            value={title}
          />
          <p className="-mt-3 mb-4 text-xs text-muted">
            Leave it blank to show the filename, {filenameTitle(entry.filename)}
            .
          </p>
          <Button disabled={!dirty} type="submit">
            {update.isPending ? "Saving…" : "Save title"}
          </Button>
        </fieldset>
      </Form>
      <section
        aria-labelledby="video-chapters-heading"
        className="mt-6 border-t border-border pt-5"
      >
        <h3 className="text-xs font-medium" id="video-chapters-heading">
          Chapters
        </h3>
        <ChapterState
          entry={entry}
          onRetry={() => retry.mutate()}
          retryError={retry.error}
          retrying={retry.isPending}
        />
      </section>
    </>
  );
}

function ChapterState({
  entry,
  onRetry,
  retrying,
  retryError,
}: {
  entry: Entry;
  onRetry: () => void;
  retrying: boolean;
  retryError: unknown;
}) {
  if (entry.chapter_status === "failed")
    return (
      <div className="mt-2">
        <p className="text-sm" role="alert">
          {entry.chapter_message ||
            "Chapter extraction failed. Playback still works."}
        </p>
        {retryError ? <Failure error={retryError} /> : null}
        <Button
          className="mt-3"
          disabled={retrying}
          onClick={onRetry}
          size="sm"
          variant="outline"
        >
          {retrying ? "Retrying…" : "Retry chapters"}
        </Button>
      </div>
    );
  if (entry.chapter_status === "pending")
    return (
      <p className="mt-2 text-sm text-muted" role="status">
        Reading chapters from the video. Playback works meanwhile.
      </p>
    );
  if (entry.chapters.length === 0)
    return (
      <p className="mt-2 text-sm text-muted">This video has no chapters.</p>
    );
  return (
    <ol className="mt-2 space-y-1 text-sm">
      {entry.chapters.map((chapter, index) => (
        <li
          className="flex items-baseline justify-between gap-3"
          key={`${chapter.start}-${chapter.end}-${chapter.title}`}
        >
          <span className="min-w-0 truncate">
            {chapter.title || `Chapter ${index + 1}`}
          </span>
          <span className="shrink-0 text-xs text-muted tabular-nums">
            {clock(chapter.start)}
          </span>
        </li>
      ))}
    </ol>
  );
}
