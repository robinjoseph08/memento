import { useEffect, useEffectEvent, useState } from "react";

import { useRetryChapters, useUpdateVideo } from "../../hooks/queries/albums";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { Entry } from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Failure, Field, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { clock } from "../viewer/labels";
import { AlbumImage } from "./album-image";
import { filenameTitle } from "./moment-labels";

// One video's Memento title and its chapter state. The title is global to the
// Media Item, so every Album showing the video changes with it. Chapters are
// read-only facts from the file; a failed extraction can be retried here and
// never blocks playback or publication.
export function VideoDialog({
  albumID,
  entry,
  onClose,
}: {
  albumID: string;
  entry: Entry;
  onClose: () => void;
}) {
  const update = useUpdateVideo(albumID, entry.id);
  const retry = useRetryChapters(albumID, entry.id);
  const [title, setTitle] = useState(entry.title);
  const dirty = title.trim() !== entry.title;
  useUnsavedChanges(dirty || update.isPending, true);
  const [discardOpen, setDiscardOpen] = useState(false);
  // Close only once nothing is unsaved: the dialog closes by clearing its URL
  // parameter, which the unsaved-changes guard would otherwise block. A save
  // reports success one render before the refreshed Album reaches this
  // dialog, so wait for the entry to carry the saved title.
  const [discarded, setDiscarded] = useState(false);
  const closeSettled = useEffectEvent(onClose);
  useEffect(() => {
    if ((update.isSuccess && !dirty) || discarded) closeSettled();
  }, [update.isSuccess, dirty, discarded]);
  const returnFocus = useReturnFocus();
  const errors = fieldErrors(update.error);
  function changeOpen(next: boolean) {
    if (next || update.isPending) return;
    if (dirty) setDiscardOpen(true);
    else onClose();
  }
  return (
    <>
      <Dialog onOpenChange={changeOpen} open>
        <DialogContent className="max-w-xl" onCloseAutoFocus={returnFocus}>
          <DialogTitle className="pr-8">Video details</DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            {entry.filename}. The title shows in every album with this video.
          </DialogDescription>
          <AlbumImage
            alt={entry.filename}
            className="mt-4 h-auto max-h-40 w-auto max-w-full"
            fallback="No preview available"
            src={entry.available ? entry.thumbnail_url : ""}
          />
          <Form
            aria-busy={update.isPending}
            aria-label="Video title"
            className="mt-6"
            error={update.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (update.isPending) return;
              if (!dirty) {
                onClose();
                return;
              }
              update.mutate(
                { title: title.trim() },
                {
                  // Keep the field on the stored value so a server-side
                  // normalization cannot leave the dialog looking unsaved.
                  onSuccess: (saved) =>
                    setTitle(
                      saved.moments
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
                  setTitle(event.target.value);
                }}
                placeholder={filenameTitle(entry.filename)}
                value={title}
              />
              <p className="-mt-3 mb-5 text-xs text-muted">
                Leave it blank to show the filename,{" "}
                {filenameTitle(entry.filename)}.
              </p>
              <div className="flex flex-wrap gap-2">
                <Button type="submit">
                  {update.isPending ? "Saving…" : "Save title"}
                </Button>
                <Button
                  onClick={() => changeOpen(false)}
                  type="button"
                  variant="outline"
                >
                  Cancel
                </Button>
              </div>
            </fieldset>
          </Form>
          <section
            aria-labelledby="video-chapters-heading"
            className="mt-8 border-t border-border pt-5"
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
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description="The video keeps its current title."
        onConfirm={() => {
          setTitle(entry.title);
          setDiscarded(true);
        }}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Discard this video title?"
      />
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
