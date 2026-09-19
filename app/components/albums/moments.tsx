import { Merge, Pencil } from "lucide-react";
import { useEffect, useEffectEvent, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import {
  useRefreshMomentFaces,
  useSetMomentCover,
  useUpdateMoment,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { disambiguatePhotoLabels, mediaLabel } from "../../lib/media-labels";
import { cn, withParams } from "../../lib/utils";
import type {
  AlbumDetail,
  Entry,
  Moment,
} from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Field, Form, sectionHeadingClass } from "../people/form-fields";
import { CountBadge } from "../shell/count-badge";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { RulesDialog } from "./access-rules";
import { AlbumImage } from "./album-image";
import { EntryPreview } from "./entry-preview";
import { ExcludeDialog } from "./exclusions";
import { MediaCounts } from "./media-counts";
import { MomentAccessStrip } from "./moment-access";
import { countLabel, countMedia, momentHeading } from "./moment-labels";
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
  // The Moment can be narrowed to its photos or its videos, so a Curator can
  // go through every video's title and chapters without hunting through photos.
  const kind =
    params.get("kind") === "photos" || params.get("kind") === "videos"
      ? params.get("kind")
      : "all";
  const photos = moment.entries.filter((entry) => entry.kind === "IMAGE");
  const videos = moment.entries.filter((entry) => entry.kind === "VIDEO");
  const entries =
    kind === "photos" ? photos : kind === "videos" ? videos : moment.entries;
  const visible = all ? entries : entries.slice(0, initialMediaCount);
  const visibleLabels = visible.map(mediaLabel);
  const visibleActionLabels = disambiguatePhotoLabels(visibleLabels);
  const tabs = [
    { key: "all", label: "All", count: moment.entries.length },
    { key: "photos", label: "Photos", count: photos.length },
    { key: "videos", label: "Videos", count: videos.length },
  ] as const;
  const [selection, setSelection] = useState<string[] | null>(null);
  const selecting = selection !== null;
  const selected = selection ?? [];
  const single =
    selected.length === 1
      ? moment.entries.find((entry) => entry.id === selected[0])
      : undefined;
  const editingEntry = moment.entries.find(
    (entry) => entry.id === params.get("entry"),
  );
  const editEntry = (entry: string | null) =>
    setParams((current) => withParams(current, { entry }));
  // An item kept out or moved elsewhere leaves the URL pointing at nothing;
  // clear it so a refresh does not reopen an empty dialog.
  const requestedEntry = params.get("entry");
  const clearEntry = useEffectEvent(() => editEntry(null));
  useEffect(() => {
    if (requestedEntry && !editingEntry) clearEntry();
  }, [requestedEntry, editingEntry]);
  const [renameOpen, setRenameOpen] = useState(false);
  const [coverEntry, setCoverEntry] = useState<Entry | null>(null);
  const [structure, setStructure] = useState<{
    operation: StructureOperation;
    selectedEntryIDs: string[];
  } | null>(null);
  const [excluding, setExcluding] = useState<string[] | null>(null);
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
            <MediaCounts {...countMedia(moment.entries)} />
          </p>
        </div>
        <div className="flex flex-wrap gap-1">
          <Button
            onClick={() => setRenameOpen(true)}
            size="sm"
            type="button"
            variant="ghost"
          >
            <Pencil aria-hidden="true" className="size-4" strokeWidth={1.5} />
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
            <Merge aria-hidden="true" className="size-4" strokeWidth={1.5} />
            Merge
          </Button>
        </div>
      </header>
      <div className="mt-5">
        <MomentAccessStrip
          album={album}
          moment={moment}
          onRefresh={() => refreshFaces()}
          refreshError={refresh.error}
          refreshing={refresh.isPending}
        />
      </div>
      <nav
        aria-label="Moment media kind"
        className="mt-6 flex border-b border-border"
      >
        {tabs.map((tab) => (
          <Link
            aria-current={kind === tab.key ? "page" : undefined}
            className={cn(
              "-mb-px inline-flex items-center gap-2 border-b-2 px-3 py-2 text-sm",
              kind === tab.key
                ? "border-primary text-foreground"
                : "border-transparent text-muted hover:text-foreground",
            )}
            key={tab.key}
            to={`?${withParams(params, { kind: tab.key === "all" ? null : tab.key })}`}
          >
            {tab.label}{" "}
            <CountBadge
              count={tab.count}
              tone={kind === tab.key ? "accent" : "muted"}
            />
          </Link>
        ))}
      </nav>
      <form
        aria-label={`Select media in ${moment.label}`}
        className="mt-3"
        onSubmit={(event) => event.preventDefault()}
      >
        <div className="flex min-h-10 flex-wrap items-center justify-between gap-3">
          {selecting ? (
            <label className="flex cursor-pointer items-center gap-2 text-xs">
              <input
                aria-label={`Select all ${entries.length} items`}
                checked={
                  entries.length > 0 &&
                  entries.every((entry) => selected.includes(entry.id))
                }
                className="size-4 cursor-pointer accent-primary"
                onChange={(event) =>
                  setSelection((current) =>
                    event.target.checked
                      ? [
                          ...new Set([
                            ...(current ?? []),
                            ...entries.map((entry) => entry.id),
                          ]),
                        ]
                      : (current ?? []).filter(
                          (id) => !entries.some((entry) => entry.id === id),
                        ),
                  )
                }
                type="checkbox"
              />
              {selected.length ? `${selected.length} selected` : "Select all"}
            </label>
          ) : (
            <p className="text-xs text-muted">
              {visible.length} of {countLabel(entries.length, "item", "items")}{" "}
              shown
            </p>
          )}
          <div className="flex flex-wrap items-center gap-1">
            {/* Stays available while selecting so Select all never reaches
                items the Curator has not seen. */}
            {entries.length > initialMediaCount && (
              <Button
                className="text-xs"
                onClick={() => showAll(!all)}
                size="sm"
                type="button"
                variant="ghost"
              >
                {all ? "Show fewer" : `Show all ${entries.length}`}
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
                <Button
                  disabled={selected.length === 0}
                  onClick={() => setExcluding(selected)}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  Keep out
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
                {single && (
                  <Button
                    onClick={() => editEntry(single.id)}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {single.kind === "VIDEO"
                      ? "Video details"
                      : "Photo details"}
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
        {entries.length === 0 && (
          <p className="mt-3 text-sm text-muted">
            No {kind === "all" ? "items" : kind} in this Moment.
          </p>
        )}
        <ul
          aria-label="Moment media"
          className="mt-3 flex flex-wrap items-start gap-x-2 gap-y-4"
        >
          {visible.map((entry, index) => (
            <EntryPreview
              actionLabel={visibleActionLabels[index]}
              cover={entry.id === moment.cover_entry_id}
              entry={entry}
              key={entry.id}
              label={visibleLabels[index]}
              onOpen={() => editEntry(entry.id)}
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
      {editingEntry && (
        <RulesDialog
          album={album}
          entry={editingEntry}
          moment={moment}
          onClose={() => editEntry(null)}
        />
      )}
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
      {excluding && (
        <ExcludeDialog
          album={album}
          entryIDs={excluding}
          moment={moment}
          onClose={() => setExcluding(null)}
          onSaved={() => {
            setExcluding(null);
            setSelection(null);
          }}
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
            alt={mediaLabel(entry)}
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
