import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  useRefreshMomentFaces,
  useSetMomentCover,
  useUpdateMoment,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { withParams } from "../../lib/utils";
import type {
  AlbumDetail,
  Entry,
  Moment,
} from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Field, Form, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { AlbumImage } from "./album-image";
import { EntryPreview } from "./entry-preview";
import { MomentAccessStrip } from "./moment-access";
import { countLabel, mediaCounts, momentHeading } from "./moment-labels";
import { StructureEditor, type StructureOperation } from "./structure-editor";

// A bounded overview of a Moment's media before an explicit Show all.
const initialMediaCount = 24;

// One Moment at a time: its title and counts, Rename and Merge, the access
// strip, then a full-width grid. Selection is a mode a Curator enters for a
// move, split, or cover change, so the everyday view stays a plain overview.
export function MomentPane({
  album,
  moment,
}: {
  album: AlbumDetail;
  moment: Moment;
}) {
  const [params, setParams] = useSearchParams();
  const heading = momentHeading(moment);
  const all = params.get("media") === "all";
  const visible = all
    ? moment.entries
    : moment.entries.slice(0, initialMediaCount);
  const [selection, setSelection] = useState<string[] | null>(null);
  const selecting = selection !== null;
  const selected = selection ?? [];
  const single =
    selected.length === 1
      ? moment.entries.find((entry) => entry.id === selected[0])
      : undefined;
  const [renameOpen, setRenameOpen] = useState(false);
  const [coverEntry, setCoverEntry] = useState<Entry | null>(null);
  const [structure, setStructure] = useState<{
    operation: StructureOperation;
    selectedEntryIDs: string[];
  } | null>(null);
  const refresh = useRefreshMomentFaces(album.id, moment.id);
  const refreshFaces = refresh.mutate;
  useEffect(() => {
    refreshFaces();
  }, [refreshFaces]);
  const showAll = (next: boolean) =>
    setParams((current) => withParams(current, { media: next ? "all" : null }));

  return (
    <section aria-labelledby={`moment-${moment.id}`}>
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h2 className={sectionHeadingClass} id={`moment-${moment.id}`}>
            {heading.title}
          </h2>
          <p className="mt-1 text-xs text-muted">
            {heading.date && <span className="mr-3">{heading.date}</span>}
            <span>{mediaCounts(moment.entries)}</span>
          </p>
        </div>
        <div className="flex flex-wrap gap-1">
          <Button
            onClick={() => setRenameOpen(true)}
            size="sm"
            type="button"
            variant="ghost"
          >
            Rename
          </Button>
          <Button
            disabled={album.moments.length < 2}
            onClick={() =>
              setStructure({ operation: "merge", selectedEntryIDs: [] })
            }
            size="sm"
            type="button"
            variant="ghost"
          >
            Merge
          </Button>
        </div>
      </header>
      <div className="mt-5">
        <MomentAccessStrip
          albumID={album.id}
          moment={moment}
          onRefresh={() => refreshFaces()}
          refreshError={refresh.error}
          refreshing={refresh.isPending}
        />
      </div>
      <form
        aria-label={`Select media in ${moment.label}`}
        className="mt-6"
        onSubmit={(event) => event.preventDefault()}
      >
        <div className="flex min-h-10 flex-wrap items-center justify-between gap-3">
          {selecting ? (
            <label className="flex cursor-pointer items-center gap-2 text-xs">
              <input
                aria-label={`Select all ${moment.entries.length} items`}
                checked={
                  moment.entries.length > 0 &&
                  selected.length === moment.entries.length
                }
                className="size-4 cursor-pointer accent-primary"
                onChange={(event) =>
                  setSelection(
                    event.target.checked
                      ? moment.entries.map((entry) => entry.id)
                      : [],
                  )
                }
                type="checkbox"
              />
              {selected.length ? `${selected.length} selected` : "Select all"}
            </label>
          ) : (
            <p className="text-xs text-muted">
              {visible.length} of{" "}
              {countLabel(moment.entries.length, "item", "items")} shown
            </p>
          )}
          <div className="flex flex-wrap items-center gap-1">
            {/* Stays available while selecting so Select all never reaches
                items the Curator has not seen. */}
            {moment.entries.length > initialMediaCount && (
              <Button
                className="text-xs"
                onClick={() => showAll(!all)}
                size="sm"
                type="button"
                variant="ghost"
              >
                {all ? "Show fewer" : `Show all ${moment.entries.length}`}
              </Button>
            )}
            {selecting ? (
              <>
                <Button
                  disabled={album.moments.length < 2 || selected.length === 0}
                  onClick={() =>
                    setStructure({
                      operation: "move",
                      selectedEntryIDs: selected,
                    })
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  Move
                </Button>
                <Button
                  disabled={
                    selected.length === 0 ||
                    selected.length === moment.entries.length
                  }
                  onClick={() =>
                    setStructure({
                      operation: "split",
                      selectedEntryIDs: selected,
                    })
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  Split
                </Button>
                {single && (
                  <Button
                    disabled={single.id === moment.cover_entry_id}
                    onClick={() => setCoverEntry(single)}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    Set as cover
                  </Button>
                )}
                <Button
                  onClick={() => setSelection(null)}
                  size="sm"
                  type="button"
                  variant="ghost"
                >
                  Done
                </Button>
              </>
            ) : (
              <Button
                onClick={() => setSelection([])}
                size="sm"
                type="button"
                variant="outline"
              >
                Select
              </Button>
            )}
          </div>
        </div>
        <ul
          aria-label="Moment media"
          className="mt-3 flex flex-wrap items-start gap-x-2 gap-y-4"
        >
          {visible.map((entry) => (
            <EntryPreview
              cover={entry.id === moment.cover_entry_id}
              entry={entry}
              key={entry.id}
              onSelect={
                selecting
                  ? (checked) =>
                      setSelection((current) =>
                        checked
                          ? [...new Set([...(current ?? []), entry.id])]
                          : (current ?? []).filter((id) => id !== entry.id),
                      )
                  : undefined
              }
              selected={selected.includes(entry.id)}
            />
          ))}
        </ul>
      </form>
      {renameOpen && (
        <RenameMomentDialog
          albumID={album.id}
          moment={moment}
          onOpenChange={(open) => !open && setRenameOpen(false)}
          open
        />
      )}
      {coverEntry && (
        <CoverDialog
          albumID={album.id}
          entry={coverEntry}
          moment={moment}
          onOpenChange={(open) => !open && setCoverEntry(null)}
          open
        />
      )}
      {structure && (
        <StructureEditor
          album={album}
          moment={moment}
          onClose={() => setStructure(null)}
          onSaved={() => {
            setStructure(null);
            setSelection(null);
          }}
          operation={structure.operation}
          selectedEntryIDs={structure.selectedEntryIDs}
        />
      )}
    </section>
  );
}

function RenameMomentDialog({
  albumID,
  moment,
  open,
  onOpenChange,
}: {
  albumID: string;
  moment: Moment;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const update = useUpdateMoment(albumID, moment.id);
  const [title, setTitle] = useState(moment.title);
  const [discardOpen, setDiscardOpen] = useState(false);
  const dirty = title !== moment.title;
  useUnsavedChanges(dirty || update.isPending, true);
  const errors = fieldErrors(update.error);
  function changeOpen(next: boolean) {
    if (next) onOpenChange(true);
    else if (update.isPending) return;
    else if (dirty) setDiscardOpen(true);
    else onOpenChange(false);
  }
  return (
    <>
      <Dialog onOpenChange={changeOpen} open={open}>
        <DialogContent>
          <DialogTitle>Rename Moment</DialogTitle>
          <DialogDescription className="mt-2 text-sm text-muted">
            Only Curators see this name. Clear it to use the generated date
            label.
          </DialogDescription>
          <Form
            aria-busy={update.isPending}
            aria-label="Rename Moment"
            className="mt-6"
            error={update.error}
            onSubmit={(event) => {
              event.preventDefault();
              update.mutate(
                { title },
                { onSuccess: () => onOpenChange(false) },
              );
            }}
          >
            <fieldset disabled={update.isPending}>
              <Field
                error={errors.title}
                label="Moment title"
                maxLength={200}
                name="title"
                onChange={(event) => setTitle(event.target.value)}
                value={title}
              />
              <div className="flex gap-2">
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
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description="The Moment keeps its current title."
        onConfirm={() => onOpenChange(false)}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Discard this Moment title?"
      />
    </>
  );
}

function CoverDialog({
  albumID,
  moment,
  entry,
  open,
  onOpenChange,
}: {
  albumID: string;
  moment: Moment;
  entry?: Entry;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const update = useSetMomentCover(albumID, moment.id);
  function changeOpen(next: boolean) {
    if (!next && update.isPending) return;
    onOpenChange(next);
  }
  return (
    <Dialog onOpenChange={changeOpen} open={open && !!entry}>
      <DialogContent>
        <DialogTitle>Change Moment cover?</DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Use this item as the cover for {moment.label}. No one gains or loses
          media.
        </DialogDescription>
        {entry && (
          <AlbumImage
            alt={entry.filename}
            className="mt-4 h-auto max-h-48 w-auto max-w-full"
            fallback="No preview available"
            src={entry.available ? entry.thumbnail_url : ""}
          />
        )}
        <Form
          aria-busy={update.isPending}
          aria-label="Change Moment cover"
          className="mt-6"
          error={update.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (entry)
              update.mutate(
                { entry_id: entry.id },
                { onSuccess: () => onOpenChange(false) },
              );
          }}
        >
          <fieldset className="flex gap-2" disabled={update.isPending}>
            <Button type="submit">
              {update.isPending ? "Saving…" : "Save cover"}
            </Button>
            <Button
              onClick={() => changeOpen(false)}
              type="button"
              variant="outline"
            >
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
