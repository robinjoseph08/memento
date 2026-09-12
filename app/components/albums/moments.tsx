import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

import {
  useRefreshMomentFaces,
  useSetMomentCover,
  useUpdateMoment,
} from "../../hooks/queries/albums";
import { useMediaQuery } from "../../hooks/use-media-query";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { cn } from "../../lib/utils";
import type {
  AlbumDetail,
  Entry,
  Moment,
  UndoMomentAccessRequest,
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
import { Sheet, SheetContent, SheetTitle } from "../ui/sheet";
import { AlbumImage } from "./album-image";
import { Audience } from "./audience";
import { EntryPreview } from "./entry-preview";
import { MomentAccessInspector } from "./moment-access";
import { StructureEditor, type StructureOperation } from "./structure-editor";

export function MediaCounts({ entries }: { entries: Entry[] }) {
  const photos = entries.filter((entry) => entry.kind === "IMAGE").length;
  const videos = entries.filter((entry) => entry.kind === "VIDEO").length;
  return `${photos} ${photos === 1 ? "photo" : "photos"}, ${videos} ${videos === 1 ? "video" : "videos"}`;
}

function momentHeading(moment: Moment) {
  const date = new Date(`${moment.date}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return { title: moment.label, date: "" };
  const options = {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
  } as const;
  const original = date.toLocaleDateString("en-US", options);
  const dated = date.toLocaleDateString("en-US", {
    ...options,
    weekday: "long",
  });
  return {
    title: moment.label === original ? dated : moment.label,
    date: moment.title ? dated : "",
  };
}

function setURLValues(
  current: URLSearchParams,
  values: Record<string, string | null>,
) {
  const next = new URLSearchParams(current);
  for (const [key, value] of Object.entries(values)) {
    if (value === null) next.delete(key);
    else next.set(key, value);
  }
  return next;
}

export function Moments({ album }: { album: AlbumDetail }) {
  const [params, setParams] = useSearchParams();
  const requestedMomentID = params.get("moment");
  const selectedMoment =
    album.moments.find((moment) => moment.id === requestedMomentID) ??
    album.moments[0];
  const expandedMomentID =
    requestedMomentID === "none" ? null : selectedMoment?.id;
  // Selection is a mode a Curator enters for a move, split, or cover change,
  // so the everyday view stays a plain overview.
  const noSelection = { momentID: "", active: false, entryIDs: [] as string[] };
  const [selection, setSelection] = useState(noSelection);
  const [undoState, setUndoState] = useState<{
    momentID: string;
    request: UndoMomentAccessRequest | null;
  }>({ momentID: "", request: null });
  const [renameMoment, setRenameMoment] = useState<Moment | null>(null);
  const [coverState, setCoverState] = useState<{
    moment: Moment;
    entry: Entry;
  } | null>(null);
  const [structureState, setStructureState] = useState<{
    moment: Moment;
    operation: StructureOperation;
    selectedEntryIDs: string[];
  } | null>(null);
  const selectedMomentID = selectedMoment?.id ?? "";
  // The inspector sits beside the Moments on wide screens, so the access
  // summary only needs to open the sheet on narrower ones.
  const inspectorVisible = useMediaQuery("(min-width: 1000px)");
  const refreshFaces = useRefreshMomentFaces(album.id, selectedMomentID);
  const refreshSelectedFaces = refreshFaces.mutate;
  useEffect(() => {
    if (selectedMomentID) refreshSelectedFaces();
  }, [album.id, selectedMomentID, refreshSelectedFaces]);

  if (!selectedMoment) {
    return (
      <section
        aria-labelledby="moments-heading"
        className="p-5 min-[761px]:p-7"
      >
        <h2 className={sectionHeadingClass} id="moments-heading">
          Moments
        </h2>
        <p className="py-6 text-sm text-muted">
          This Album had no photos or videos to import.
        </p>
      </section>
    );
  }

  const selecting =
    selection.momentID === selectedMoment.id && selection.active;
  const selectedEntries = selecting ? selection.entryIDs : [];
  const undo =
    undoState.momentID === selectedMoment.id ? undoState.request : null;
  const setSelectedEntries = (
    update: string[] | ((current: string[]) => string[]),
  ) =>
    setSelection((current) => {
      const currentEntries =
        current.momentID === selectedMoment.id ? current.entryIDs : [];
      return {
        momentID: selectedMoment.id,
        active: true,
        entryIDs:
          typeof update === "function" ? update(currentEntries) : update,
      };
    });
  const setUndo = (request: UndoMomentAccessRequest | null) =>
    setUndoState({ momentID: selectedMoment.id, request });
  const sheetOpen = params.get("inspect") === "1";
  const updateURL = (values: Record<string, string | null>) =>
    setParams((current) => setURLValues(current, values));
  const access = (
    <MomentAccessInspector
      albumID={album.id}
      key={selectedMoment.id}
      moment={selectedMoment}
      onRefresh={() => refreshSelectedFaces()}
      onUndo={setUndo}
      refreshError={refreshFaces.error}
      refreshing={refreshFaces.isPending}
      undo={undo}
    />
  );

  return (
    <div className="min-[1000px]:grid min-[1000px]:grid-cols-[minmax(0,1fr)_320px]">
      <section
        aria-labelledby="moments-heading"
        className="min-w-0 p-3 py-6 min-[761px]:p-6 min-[761px]:py-7"
      >
        <div className="flex items-center justify-between gap-4">
          <h2 className={sectionHeadingClass} id="moments-heading">
            Moments
          </h2>
          <p className="text-xs text-muted">
            {album.moments.length}{" "}
            {album.moments.length === 1 ? "Moment" : "Moments"}
          </p>
        </div>
        <p className="mt-2 mb-6 max-w-180 text-xs leading-relaxed text-muted">
          Review access by Moment. Select media when you need to move, split, or
          choose a cover.
        </p>
        <div className="space-y-3">
          {album.moments.map((moment) => {
            const expanded = moment.id === expandedMomentID;
            const heading = momentHeading(moment);
            const momentCover = moment.entries.find(
              (entry) => entry.id === moment.cover_entry_id,
            );
            const all = expanded && params.get("media") === "all";
            const visible = expanded
              ? all
                ? moment.entries
                : moment.entries.slice(0, 24)
              : [];
            const selected = selectedEntries.filter((id) =>
              moment.entries.some((entry) => entry.id === id),
            );
            const suggestions = moment.access.people.filter(
              (person) => person.suggested,
            ).length;
            const openSheet = () =>
              updateURL({
                moment: moment.id,
                inspect: "1",
                media: null,
              });
            return (
              <article
                aria-label={heading.title}
                className={cn(
                  "overflow-hidden rounded-md border",
                  expanded ? "border-primary" : "border-border",
                )}
                key={moment.id}
                role="region"
              >
                <header className="flex bg-surface">
                  <h3 className="min-w-0 flex-1">
                    <button
                      aria-expanded={expanded}
                      aria-label={heading.title}
                      className="flex w-full cursor-pointer items-center gap-3 px-3 py-3 text-left hover:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                      id={`moment-${moment.id}`}
                      onClick={() =>
                        updateURL({
                          moment: expanded ? "none" : moment.id,
                          inspect: null,
                          media: null,
                        })
                      }
                      type="button"
                    >
                      <AlbumImage
                        alt=""
                        className="h-auto max-h-11 w-auto max-w-20 shrink-0"
                        fallback="No cover"
                        src={
                          momentCover?.available
                            ? momentCover.thumbnail_url
                            : ""
                        }
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block font-heading text-xl leading-tight min-[761px]:text-2xl">
                          {heading.title}
                        </span>
                        {heading.date && (
                          <span className="mt-1 block text-xs font-normal text-muted">
                            {heading.date}
                          </span>
                        )}
                        <span className="mt-1 block text-xs font-normal text-muted">
                          <MediaCounts entries={moment.entries} />
                        </span>
                      </span>
                      <ChevronRight
                        aria-hidden="true"
                        className={cn(
                          "size-4 shrink-0",
                          expanded && "rotate-90",
                        )}
                        strokeWidth={1.5}
                      />
                    </button>
                  </h3>
                  {inspectorVisible ? (
                    <div className="hidden min-w-28 items-center border-l border-border px-3 py-2 text-xs min-[601px]:flex">
                      <Audience
                        interactive={false}
                        people={moment.access.people}
                        suggestions={suggestions}
                      />
                    </div>
                  ) : (
                    <button
                      aria-label={`Access for ${moment.label}`}
                      className="hidden min-w-28 cursor-pointer items-center border-l border-border px-3 py-2 text-xs hover:bg-accent min-[601px]:flex"
                      onClick={openSheet}
                      type="button"
                    >
                      <Audience
                        interactive
                        people={moment.access.people}
                        suggestions={suggestions}
                      />
                    </button>
                  )}
                </header>
                <button
                  aria-label={`Access for ${moment.label}`}
                  className="flex w-full cursor-pointer items-center justify-between border-t border-border bg-surface px-3 py-2 text-left text-xs hover:bg-accent min-[601px]:hidden"
                  onClick={openSheet}
                  type="button"
                >
                  <span className="text-accent-foreground">Access</span>
                  <Audience
                    interactive
                    people={moment.access.people}
                    suggestions={suggestions}
                  />
                </button>
                {expanded && (
                  <>
                    <form
                      aria-label={`Select media in ${moment.label}`}
                      className="border-t border-border"
                      onSubmit={(event) => event.preventDefault()}
                    >
                      <div className="flex min-h-12 flex-wrap items-center justify-between gap-3 border-b border-border px-3 py-2">
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
                                setSelectedEntries(
                                  event.target.checked
                                    ? moment.entries.map((entry) => entry.id)
                                    : [],
                                )
                              }
                              type="checkbox"
                            />
                            {selected.length
                              ? `${selected.length} selected`
                              : "Select all"}
                          </label>
                        ) : (
                          <Button
                            onClick={() => setSelectedEntries([])}
                            size="sm"
                            type="button"
                            variant="outline"
                          >
                            Select
                          </Button>
                        )}
                        <div className="flex flex-wrap items-center justify-end gap-1">
                          {selecting ? (
                            <>
                              <Button
                                disabled={
                                  album.moments.length < 2 ||
                                  selected.length === 0
                                }
                                onClick={() =>
                                  setStructureState({
                                    moment,
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
                                  setStructureState({
                                    moment,
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
                              {selected.length === 1 && (
                                <Button
                                  disabled={
                                    selected[0] === moment.cover_entry_id
                                  }
                                  onClick={() => {
                                    const entry = moment.entries.find(
                                      (item) => item.id === selected[0],
                                    );
                                    if (entry) setCoverState({ moment, entry });
                                  }}
                                  size="sm"
                                  type="button"
                                  variant="outline"
                                >
                                  Set as cover
                                </Button>
                              )}
                              <Button
                                onClick={() => setSelection(noSelection)}
                                size="sm"
                                type="button"
                                variant="ghost"
                              >
                                Done
                              </Button>
                            </>
                          ) : (
                            <>
                              <Button
                                onClick={() => setRenameMoment(moment)}
                                size="sm"
                                type="button"
                                variant="ghost"
                              >
                                Rename
                              </Button>
                              <Button
                                disabled={album.moments.length < 2}
                                onClick={() =>
                                  setStructureState({
                                    moment,
                                    operation: "merge",
                                    selectedEntryIDs: [],
                                  })
                                }
                                size="sm"
                                type="button"
                                variant="ghost"
                              >
                                Merge
                              </Button>
                            </>
                          )}
                        </div>
                      </div>
                      <ul
                        aria-label="Moment media"
                        className="flex flex-wrap items-start gap-x-2 gap-y-4 p-3"
                      >
                        {visible.map((entry) => (
                          <EntryPreview
                            cover={entry.id === moment.cover_entry_id}
                            entry={entry}
                            key={entry.id}
                            onSelect={
                              selecting
                                ? (checked) =>
                                    setSelectedEntries((current) =>
                                      checked
                                        ? [...new Set([...current, entry.id])]
                                        : current.filter(
                                            (id) => id !== entry.id,
                                          ),
                                    )
                                : undefined
                            }
                            selected={selected.includes(entry.id)}
                          />
                        ))}
                      </ul>
                    </form>
                    <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-3 py-3 text-xs text-muted">
                      <span>
                        {visible.length} of {moment.entries.length} items shown
                      </span>
                      {moment.entries.length > 24 && (
                        <Button
                          className="text-xs"
                          onClick={() =>
                            updateURL({ media: all ? null : "all" })
                          }
                          variant="ghost"
                        >
                          {all
                            ? "Show fewer items"
                            : `Show all ${moment.entries.length} items`}
                        </Button>
                      )}
                    </footer>
                  </>
                )}
              </article>
            );
          })}
        </div>
      </section>
      {inspectorVisible && (
        <aside
          aria-label="Moment access"
          className="border-l border-border p-5"
        >
          {access}
        </aside>
      )}
      <Sheet
        onOpenChange={(open) => !open && updateURL({ inspect: null })}
        open={sheetOpen && !inspectorVisible}
      >
        <SheetContent
          className="right-0 left-auto w-[min(25rem,calc(100vw-1rem))] border-r-0 border-l border-border"
          closeLabel="Close panel"
        >
          <SheetTitle className="mb-7 font-heading text-[27px]/[1.2] font-normal">
            Moment access
          </SheetTitle>
          {access}
        </SheetContent>
      </Sheet>
      {renameMoment && (
        <RenameMomentDialog
          albumID={album.id}
          moment={renameMoment}
          onOpenChange={(open) => !open && setRenameMoment(null)}
          open
        />
      )}
      {coverState && (
        <CoverDialog
          albumID={album.id}
          entry={coverState.entry}
          moment={coverState.moment}
          onOpenChange={(open) => !open && setCoverState(null)}
          open
        />
      )}
      {structureState && (
        <StructureEditor
          album={album}
          moment={structureState.moment}
          onClose={() => setStructureState(null)}
          onSaved={() => {
            setStructureState(null);
            setSelection(noSelection);
          }}
          operation={structureState.operation}
          selectedEntryIDs={structureState.selectedEntryIDs}
        />
      )}
    </div>
  );
}

export function RenameMomentDialog({
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

export function CoverDialog({
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
