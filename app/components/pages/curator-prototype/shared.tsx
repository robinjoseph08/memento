// PROTOTYPE. Pieces every variant composes differently: hooks for the ticket
// 10 endpoints the stub serves, the access inspector with inherited access
// and Rules & exceptions, Album access, Viewer preview, and Review & publish.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Image, RefreshCw, SquarePlay } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";

import {
  useAddMomentSuggestions,
  useSetMomentAccess,
  useUndoMomentAccess,
} from "../../../hooks/queries/albums";
import { usePrivateScope } from "../../../hooks/queries/people";
import { useUnsavedChanges } from "../../../hooks/use-unsaved-changes";
import { fieldErrors, request } from "../../../lib/http";
import { initials } from "../../../lib/initials";
import { cn } from "../../../lib/utils";
import type {
  Decision,
  Entry,
  Moment,
  StructurePreview,
  UndoMomentAccessRequest,
} from "../../../types/generated/publishing";
import { AlbumImage } from "../../albums/album-image";
import { UnlinkedFaces } from "../../albums/face-management";
import { CoverDialog, RenameMomentDialog } from "../../albums/moments";
import {
  StructureEditor,
  type StructureOperation,
} from "../../albums/structure-editor";
import { ConfirmDialog } from "../../forms/confirm-dialog";
import {
  Failure,
  FieldError,
  Form,
  headingClass,
  sectionHeadingClass,
} from "../../people/form-fields";
import { Avatar, AvatarFallback, AvatarImage } from "../../ui/avatar";
import { Button } from "../../ui/button";
import { Combobox } from "../../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../../ui/dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../../ui/tooltip";
import type {
  PrototypeAccessPerson,
  PrototypeAlbum,
  PrototypeEntry,
  PrototypeMoment,
  ViewerAlbum,
} from "./model";

export type { PrototypeAlbum, PrototypeMoment } from "./model";

export function withParams(
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

export function useURLState() {
  const [params, setParams] = useSearchParams();
  return {
    params,
    update: (values: Record<string, string | null>) =>
      setParams((current) => withParams(current, values)),
    link: (values: Record<string, string | null>) =>
      `?${withParams(params, values)}`,
  };
}

export function countLabel(count: number, singular: string, plural: string) {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function mediaCounts(entries: Entry[]) {
  const photos = entries.filter((entry) => entry.kind === "IMAGE").length;
  const videos = entries.filter((entry) => entry.kind === "VIDEO").length;
  return videos
    ? `${countLabel(photos, "photo", "photos")}, ${countLabel(videos, "video", "videos")}`
    : countLabel(photos, "photo", "photos");
}

export function dayLabel(day: string, weekday = false) {
  const date = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
    ...(weekday ? { weekday: "long" } : {}),
  });
}

// Mirrors the private helper in albums/moments.tsx: titled Moments show their
// date beneath, untitled ones use the weekday date as the title.
export function momentHeading(moment: Moment) {
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

export function dateRange(start: string, end: string) {
  if (!start) return "";
  return start === end
    ? dayLabel(start)
    : `${dayLabel(start)} to ${dayLabel(end)}`;
}

export function pendingSuggestions(album: PrototypeAlbum) {
  return album.moments.reduce(
    (count, moment) =>
      count + moment.access.people.filter((person) => person.suggested).length,
    0,
  );
}

export function unlinkedFaceCount(moment: PrototypeMoment) {
  return moment.access.faces.filter((face) => !face.person_id && !face.ignored)
    .length;
}

// Album-level mutations save the returned album straight into the cache.
function useAlbumSave(albumID: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return {
    scope,
    save: async (album: PrototypeAlbum) => {
      client.setQueryData([...scope, "album", albumID], album);
      await client.invalidateQueries({ queryKey: [...scope, "albums"] });
    },
  };
}

function albumURL(albumID: string, path: string) {
  return `/api/curator/albums/${encodeURIComponent(albumID)}/${path}`;
}

export function useAlbumAction<TBody = Record<string, never>>(
  albumID: string,
  path: string,
) {
  const cache = useAlbumSave(albumID);
  return useMutation({
    mutationKey: cache.scope,
    mutationFn: (body: TBody) =>
      request<PrototypeAlbum>(albumURL(albumID, path), { body }),
    onSuccess: cache.save,
  });
}

export function useAlbumReview<TBody>(albumID: string, path: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: TBody) =>
      request<StructurePreview>(albumURL(albumID, path), { body }),
  });
}

export function useViewerPreview(albumID: string, personID: string) {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "album", albumID, "preview", personID],
    queryFn: ({ signal }) =>
      request<ViewerAlbum>(
        `${albumURL(albumID, "preview")}?person=${encodeURIComponent(personID)}`,
        { signal },
      ),
    enabled: !!personID,
    retry: false,
  });
}

export function PersonAvatar({
  person,
  className,
}: {
  person: { display_name: string; avatar_url: string };
  className?: string;
}) {
  return (
    <Avatar className={className}>
      {person.avatar_url && <AvatarImage alt="" src={person.avatar_url} />}
      <AvatarFallback className="text-[10px]">
        {initials(person.display_name)}
      </AvatarFallback>
    </Avatar>
  );
}

// ---------------------------------------------------------------------------
// Moment dialogs shared by every variant: rename, cover, structure, rules.

type DialogState =
  | { kind: "rename"; moment: PrototypeMoment }
  | { kind: "cover"; moment: PrototypeMoment; entry: Entry }
  | {
      kind: "structure";
      moment: PrototypeMoment;
      operation: StructureOperation;
      entryIDs: string[];
    }
  | { kind: "rules"; moment: PrototypeMoment; entry?: PrototypeEntry }
  | { kind: "publish" }
  | null;

export function useMomentDialogs(album: PrototypeAlbum) {
  const [state, setState] = useState<DialogState>(null);
  const [onSaved, setOnSaved] = useState<(() => void) | null>(null);
  const close = () => {
    setState(null);
    setOnSaved(null);
  };
  const element = (
    <>
      {state?.kind === "rename" && (
        <RenameMomentDialog
          albumID={album.id}
          moment={state.moment}
          onOpenChange={(open) => !open && close()}
          open
        />
      )}
      {state?.kind === "cover" && (
        <CoverDialog
          albumID={album.id}
          entry={state.entry}
          moment={state.moment}
          onOpenChange={(open) => !open && close()}
          open
        />
      )}
      {state?.kind === "structure" && (
        <StructureEditor
          album={album}
          moment={state.moment}
          onClose={close}
          onSaved={() => {
            onSaved?.();
            close();
          }}
          operation={state.operation}
          selectedEntryIDs={state.entryIDs}
        />
      )}
      {state?.kind === "rules" && (
        <RulesDialog
          album={album}
          entry={state.entry}
          moment={state.moment}
          onClose={close}
        />
      )}
      {state?.kind === "publish" && (
        <PublishDialog album={album} onClose={close} />
      )}
    </>
  );
  return {
    element,
    rename: (moment: PrototypeMoment) => setState({ kind: "rename", moment }),
    cover: (moment: PrototypeMoment, entry: Entry) =>
      setState({ kind: "cover", moment, entry }),
    structure: (
      moment: PrototypeMoment,
      operation: StructureOperation,
      entryIDs: string[],
      afterSave?: () => void,
    ) => {
      setOnSaved(() => afterSave ?? null);
      setState({ kind: "structure", moment, operation, entryIDs });
    },
    rules: (moment: PrototypeMoment, entry?: PrototypeEntry) =>
      setState({ kind: "rules", moment, entry }),
    publish: () => setState({ kind: "publish" }),
  };
}

export type MomentDialogs = ReturnType<typeof useMomentDialogs>;

// Selection is a mode entered for a move, split, cover, or item exception.
export function useSelection() {
  const [state, setState] = useState<{
    momentID: string;
    entryIDs: string[];
  } | null>(null);
  return {
    momentID: state?.momentID ?? null,
    entryIDs: state?.entryIDs ?? [],
    start: (momentID: string) => setState({ momentID, entryIDs: [] }),
    clear: () => setState(null),
    set: (momentID: string, entryIDs: string[]) =>
      setState({ momentID, entryIDs }),
    toggle: (momentID: string, entryID: string, checked: boolean) =>
      setState((current) => {
        const entryIDs = current?.momentID === momentID ? current.entryIDs : [];
        return {
          momentID,
          entryIDs: checked
            ? [...new Set([...entryIDs, entryID])]
            : entryIDs.filter((id) => id !== entryID),
        };
      }),
  };
}

export type Selection = ReturnType<typeof useSelection>;

// The actions available once media is selected in a Moment.
export function SelectionActions({
  album,
  moment,
  selection,
  dialogs,
  size = "sm",
  variant = "outline",
}: {
  album: PrototypeAlbum;
  moment: PrototypeMoment;
  selection: Selection;
  dialogs: MomentDialogs;
  size?: "sm" | "default";
  variant?: "outline" | "ghost";
}) {
  const selected = selection.entryIDs;
  const single =
    selected.length === 1
      ? moment.entries.find((entry) => entry.id === selected[0])
      : undefined;
  return (
    <>
      <Button
        disabled={album.moments.length < 2 || selected.length === 0}
        onClick={() =>
          dialogs.structure(moment, "move", selected, selection.clear)
        }
        size={size}
        variant={variant}
      >
        Move
      </Button>
      <Button
        disabled={
          selected.length === 0 || selected.length === moment.entries.length
        }
        onClick={() =>
          dialogs.structure(moment, "split", selected, selection.clear)
        }
        size={size}
        variant={variant}
      >
        Split
      </Button>
      {single && (
        <>
          <Button
            disabled={single.id === moment.cover_entry_id}
            onClick={() => dialogs.cover(moment, single)}
            size={size}
            variant={variant}
          >
            Set as cover
          </Button>
          <Button
            onClick={() => dialogs.rules(moment, single)}
            size={size}
            variant={variant}
          >
            Item access
          </Button>
        </>
      )}
      <Button onClick={selection.clear} size={size} variant="ghost">
        Done
      </Button>
    </>
  );
}

// ---------------------------------------------------------------------------
// Access inspector. A column by default; "strip" lays the groups side by side.

function byPresence(left: PrototypeAccessPerson, right: PrototypeAccessPerson) {
  if (left.supporting_entries !== right.supporting_entries)
    return right.supporting_entries - left.supporting_entries;
  return left.display_name.localeCompare(right.display_name);
}

export function accessDetail(person: PrototypeAccessPerson) {
  const seen = person.detected
    ? `seen in ${countLabel(person.supporting_entries, "item", "items")}`
    : "not seen in this Moment";
  if (person.inherited) return `Album access, ${seen}`;
  if (person.suggested) return `Detected here, not shared yet`;
  return seen.charAt(0).toUpperCase() + seen.slice(1);
}

function AccessGroup({
  id,
  title,
  count,
  action,
  children,
}: {
  id: string;
  title: string;
  count: number;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="min-w-0">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium" id={id}>
          {title}{" "}
          <span className="ml-1 text-xs font-normal text-muted">{count}</span>
        </h3>
        {action}
      </div>
      <div className="mt-1">{children}</div>
    </section>
  );
}

export function useMomentAccess(albumID: string, moment: PrototypeMoment) {
  const change = useSetMomentAccess(albumID, moment.id);
  const addSuggestions = useAddMomentSuggestions(albumID, moment.id);
  const undoChange = useUndoMomentAccess(albumID, moment.id);
  const [undo, setUndo] = useState<UndoMomentAccessRequest | null>(null);
  const pending =
    change.isPending || addSuggestions.isPending || undoChange.isPending;
  return {
    pending,
    error: change.error ?? addSuggestions.error ?? undoChange.error,
    canUndo: !!undo?.changes.length,
    undoing: undoChange.isPending,
    decide: (personID: string, decision: Decision) =>
      change.mutate(
        { person_id: personID, decision },
        { onSuccess: (result) => setUndo(result.undo) },
      ),
    acceptSuggestions: () =>
      addSuggestions.mutate(undefined, {
        onSuccess: (result) => setUndo(result.undo),
      }),
    undo: () =>
      undo && undoChange.mutate(undo, { onSuccess: () => setUndo(null) }),
  };
}

export function AccessInspector({
  album,
  moment,
  layout = "column",
  heading = true,
  refresh,
  onRules,
}: {
  album: PrototypeAlbum;
  moment: PrototypeMoment;
  layout?: "column" | "strip";
  heading?: boolean;
  refresh: { pending: boolean; error: Error | null; run: () => void };
  onRules: () => void;
}) {
  const access = useMomentAccess(album.id, moment);
  const people = [...moment.access.people].sort(byPresence);
  const allowed = people.filter((person) => person.decision === "allow");
  const suggested = people.filter((person) => person.suggested);
  const excluded = people.filter((person) => person.decision === "deny");
  const others = people.filter(
    (person) => !person.decision && !person.suggested,
  );
  const refreshedAt = moment.access.refreshed_at
    ? new Date(moment.access.refreshed_at).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "";
  const strip = layout === "strip";

  const personRow = (person: PrototypeAccessPerson) => (
    <label
      className="flex cursor-pointer items-center gap-3 border-t border-border py-3 text-sm"
      key={person.person_id}
    >
      <input
        aria-label={`Allow ${person.display_name} for this Moment`}
        checked={person.decision === "allow"}
        className="size-4 cursor-pointer accent-primary"
        onChange={(event) =>
          access.decide(
            person.person_id,
            event.target.checked ? "allow" : "deny",
          )
        }
        type="checkbox"
      />
      <PersonAvatar person={person} />
      <span className="min-w-0">
        <strong className="block truncate font-medium">
          {person.display_name}
        </strong>
        <small className="block text-xs text-muted">
          {accessDetail(person)}
        </small>
      </span>
    </label>
  );

  const unresolvedFaces = unlinkedFaceCount(moment);
  const rulesButton = (
    <Button
      className={cn(
        "-mx-2 h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground",
        !strip && "mt-3",
      )}
      onClick={onRules}
      variant="ghost"
    >
      Rules & exceptions
    </Button>
  );
  const undoButton = (
    <Button
      className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
      disabled={!access.canUndo || access.pending}
      onClick={access.undo}
      variant="ghost"
    >
      {access.undoing ? "Undoing…" : "Undo"}
    </Button>
  );

  return (
    <div className="min-w-0">
      {heading && (
        <>
          <p className="text-xs text-muted">Moment access</p>
          <h2 className={cn("mt-1", sectionHeadingClass)}>{moment.label}</h2>
        </>
      )}
      <div
        className={cn(
          "flex items-center justify-between gap-3",
          heading && "mt-3",
        )}
      >
        <p className="text-xs text-muted">Changes save immediately.</p>
        <span className="flex items-center gap-3">
          {strip && rulesButton}
          {undoButton}
        </span>
      </div>
      <Failure error={access.error} />
      <form
        aria-label="Quick Moment access"
        className={cn(strip ? "mt-3" : "mt-6")}
        onSubmit={(event) => event.preventDefault()}
      >
        <fieldset
          className={cn(
            strip
              ? "grid gap-x-8 gap-y-5 min-[1000px]:grid-cols-2"
              : "space-y-6",
          )}
          disabled={access.pending}
        >
          <AccessGroup
            count={allowed.length}
            id="allowed-access"
            title="Allowed"
          >
            {allowed.length > 0 ? (
              allowed.map(personRow)
            ) : (
              <p className="border-t border-border py-3 text-xs text-muted">
                No one can see this Moment yet.
              </p>
            )}
          </AccessGroup>
          {(suggested.length > 0 || strip) && (
            <AccessGroup
              action={
                suggested.length > 0 && (
                  <Button
                    className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
                    onClick={access.acceptSuggestions}
                    variant="ghost"
                  >
                    Add all suggested
                  </Button>
                )
              }
              count={suggested.length}
              id="suggested-access"
              title="Suggested"
            >
              {suggested.length > 0 ? (
                suggested.map(personRow)
              ) : (
                <p className="border-t border-border py-3 text-xs text-muted">
                  No new faces to review.
                </p>
              )}
            </AccessGroup>
          )}
          {excluded.length > 0 && (
            <AccessGroup
              count={excluded.length}
              id="excluded-access"
              title="Excluded"
            >
              {excluded.map(personRow)}
            </AccessGroup>
          )}
          {others.length > 0 && (
            <details className={cn(strip && "min-[1000px]:col-span-2")}>
              <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
                Add someone else
              </summary>
              {others.map(personRow)}
            </details>
          )}
        </fieldset>
      </form>
      {!strip && rulesButton}
      {strip ? (
        unresolvedFaces > 0 && (
          <details className="mt-3">
            <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-1 text-xs text-accent-foreground hover:bg-surface">
              {countLabel(unresolvedFaces, "unlinked face", "unlinked faces")}{" "}
              to link
            </summary>
            <UnlinkedFaces faces={moment.access.faces} />
          </details>
        )
      ) : (
        <UnlinkedFaces faces={moment.access.faces} />
      )}
      <div
        className={cn(
          "border-t border-border pt-4 text-xs text-muted",
          strip ? "mt-4 flex flex-wrap items-start gap-x-6 gap-y-2" : "mt-6",
        )}
      >
        <div className="flex items-center justify-between gap-3">
          {refresh.pending ? (
            <p role="status">Checking Immich for faces…</p>
          ) : refresh.error ? (
            <p className="text-destructive" role="alert">
              Couldn't check Immich for faces.
              {refreshedAt && ` Showing faces from ${refreshedAt}.`}
            </p>
          ) : (
            <p>
              {refreshedAt ? (
                <>
                  Faces checked{" "}
                  <time dateTime={moment.access.refreshed_at ?? undefined}>
                    {refreshedAt}
                  </time>
                </>
              ) : (
                "Faces not checked yet."
              )}
            </p>
          )}
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  aria-label="Check Immich for faces again"
                  className="size-8 shrink-0 p-0"
                  disabled={refresh.pending}
                  onClick={refresh.run}
                  size="sm"
                  variant="ghost"
                >
                  <RefreshCw
                    aria-hidden="true"
                    className="size-4"
                    strokeWidth={1.5}
                  />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">Check again</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
        <details className={cn(strip ? "min-w-0 flex-1 basis-60" : "mt-3")}>
          <summary
            className={cn(
              "-mx-2 cursor-pointer rounded-sm px-2 text-foreground hover:bg-surface",
              // Matches the refresh button's height so the collapsed row
              // reads as one centered line while the open one stays anchored.
              strip ? "py-[7px]" : "py-1",
            )}
          >
            How access works
          </summary>
          <p className="mt-2 leading-relaxed">
            Checking a person allows this Moment. Unchecking excludes them, even
            when they have Album access. Item exceptions still win. Faces Immich
            recognized only suggest access; nothing changes until you choose.
          </p>
        </details>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Rules & exceptions: allow, deny, or inherit for a Moment or a single item.

export function RulesDialog({
  album,
  moment,
  entry,
  onClose,
}: {
  album: PrototypeAlbum;
  moment: PrototypeMoment;
  entry?: PrototypeEntry;
  onClose: () => void;
}) {
  const save = useAlbumAction<{
    decisions: { person_id: string; decision: Decision }[];
  }>(
    album.id,
    entry ? `entries/${entry.id}/rules` : `moments/${moment.id}/rules`,
  );
  const people = [...moment.access.people].sort(byPresence);
  const saved = (person: PrototypeAccessPerson) => {
    const current = entry
      ? entry.decisions[person.person_id]
      : person.inherited
        ? ""
        : person.decision;
    return current === "allow" || current === "deny" ? current : "inherit";
  };
  // The form opens on saved decisions only. Suggestions stay suggestions
  // until the Curator chooses them here or with Add all suggested.
  const [draft, setDraft] = useState<Record<string, Decision>>(() =>
    Object.fromEntries(
      people.map((person) => [person.person_id, saved(person)]),
    ),
  );
  const dirty =
    save.isPending ||
    people.some((person) => draft[person.person_id] !== saved(person));
  useUnsavedChanges(dirty, true);
  const [discardOpen, setDiscardOpen] = useState(false);
  const errors = fieldErrors(save.error);
  const inheritedLabel = (person: PrototypeAccessPerson) => {
    const albumAllowed = album.people.find(
      (item) => item.person_id === person.person_id,
    )?.album_allowed;
    if (!entry)
      return albumAllowed
        ? "Inherit: allowed by Album access"
        : "Inherit: no access";
    if (person.decision === "allow")
      return person.inherited
        ? "Inherit: allowed by Album access"
        : "Inherit: allowed by this Moment";
    if (person.decision === "deny") return "Inherit: excluded by this Moment";
    return "Inherit: no access";
  };
  function changeOpen(next: boolean) {
    if (next || save.isPending) return;
    if (dirty) setDiscardOpen(true);
    else onClose();
  }
  return (
    <>
      <Dialog onOpenChange={changeOpen} open>
        <DialogContent className="max-w-xl">
          <DialogTitle className="pr-8">
            {entry ? "Item access" : "Rules & exceptions"}
          </DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            {entry
              ? `Decisions for ${entry.filename} override its Moment and the Album.`
              : `Decisions for ${moment.label} override Album access. Item exceptions still win.`}
          </DialogDescription>
          {entry && (
            <AlbumImage
              alt={entry.filename}
              className="mt-4 h-auto max-h-40 w-auto max-w-full"
              fallback="No preview available"
              src={entry.available ? entry.thumbnail_url : ""}
            />
          )}
          <Form
            aria-busy={save.isPending}
            aria-label={entry ? "Item access" : "Rules & exceptions"}
            className="mt-6"
            error={save.error}
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(
                {
                  decisions: people.map((person) => ({
                    person_id: person.person_id,
                    decision: draft[person.person_id],
                  })),
                },
                { onSuccess: onClose },
              );
            }}
          >
            <fieldset disabled={save.isPending}>
              {people.map((person) => (
                <RuleRow
                  error={errors[person.person_id]}
                  inheritedLabel={inheritedLabel(person)}
                  key={person.person_id}
                  onChange={(decision) =>
                    setDraft((current) => ({
                      ...current,
                      [person.person_id]: decision,
                    }))
                  }
                  person={person}
                  scopeNote={entry ? "" : accessDetail(person)}
                  value={draft[person.person_id]}
                />
              ))}
              <div className="mt-6 flex flex-wrap gap-2">
                <Button type="submit">
                  {save.isPending ? "Saving…" : "Save access"}
                </Button>
                <Button onClick={() => changeOpen(false)} variant="outline">
                  Cancel
                </Button>
              </div>
            </fieldset>
          </Form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description="Saved access stays as it is."
        onConfirm={onClose}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Discard these access changes?"
      />
    </>
  );
}

function RuleRow({
  person,
  value,
  onChange,
  inheritedLabel,
  scopeNote,
  error,
}: {
  person: PrototypeAccessPerson;
  value: Decision;
  onChange: (decision: Decision) => void;
  inheritedLabel: string;
  scopeNote: string;
  error?: string;
}) {
  const id = useId();
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-1 border-t border-border py-3 min-[601px]:grid-cols-[auto_minmax(0,1fr)_200px]">
      <PersonAvatar person={person} />
      <span className="min-w-0" id={`${id}-label`}>
        <strong className="block truncate text-sm font-medium">
          {person.display_name}
        </strong>
        {scopeNote && (
          <small className="block text-xs text-muted">{scopeNote}</small>
        )}
      </span>
      <div className="col-span-2 min-[601px]:col-span-1">
        <Combobox
          aria-describedby={error ? `${id}-error` : undefined}
          aria-invalid={!!error}
          aria-labelledby={`${id}-label`}
          onChange={(next) => onChange(next as Decision)}
          options={[
            { value: "inherit", label: inheritedLabel },
            { value: "allow", label: "Allow" },
            { value: "deny", label: "Deny" },
          ]}
          value={value}
        />
        <FieldError error={error} id={`${id}-error`} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Album access: broad allows with an explicit Save, plus removing someone.

export function VisibilityReview({
  preview,
  album,
  className,
}: {
  preview: StructurePreview | null | undefined;
  album: PrototypeAlbum;
  className?: string;
}) {
  return (
    <section
      aria-label="Visibility review"
      className={cn("border-y border-border py-5", className)}
    >
      <h3 className="font-heading text-xl">Visibility after saving</h3>
      {!preview ? (
        <p className="mt-3 text-sm text-muted">
          Change access above to review it.
        </p>
      ) : preview.changes.length === 0 ? (
        <p className="mt-3 text-sm text-muted">No one gains or loses media.</p>
      ) : (
        <div className="mt-3 space-y-2">
          {preview.changes.map((change) => (
            <p className="py-1 text-sm" key={change.person_id}>
              <strong>{change.display_name}</strong>{" "}
              <span className="text-muted">
                {change.gained_entry_ids.length > 0 &&
                  `gains ${change.gained_entry_ids.length}`}
                {change.gained_entry_ids.length > 0 &&
                  change.lost_entry_ids.length > 0 &&
                  ", "}
                {change.lost_entry_ids.length > 0 &&
                  `loses ${change.lost_entry_ids.length}`}
              </span>
            </p>
          ))}
        </div>
      )}
      {!album.published && (
        <p className="mt-3 text-xs text-muted">
          This album is unpublished. These changes apply to the view after
          publication.
        </p>
      )}
    </section>
  );
}

export function AlbumAccessSection({
  album,
  className,
}: {
  album: PrototypeAlbum;
  className?: string;
}) {
  const savedAccess = Object.fromEntries(
    album.people.map((person) => [person.person_id, person.album_allowed]),
  );
  const [draft, setDraft] = useState(savedAccess);
  const review = useAlbumReview<{
    people: { person_id: string; allowed: boolean }[];
  }>(album.id, "access/preview");
  const save = useAlbumAction<{
    people: { person_id: string; allowed: boolean }[];
  }>(album.id, "access/save");
  const [removing, setRemoving] = useState<string | null>(null);
  const dirty =
    save.isPending ||
    album.people.some(
      (person) => draft[person.person_id] !== savedAccess[person.person_id],
    );
  useUnsavedChanges(dirty);
  const payload = (next: Record<string, boolean>) => ({
    people: album.people.map((person) => ({
      person_id: person.person_id,
      allowed: !!next[person.person_id],
    })),
  });
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  const removingPerson = album.people.find(
    (person) => person.person_id === removing,
  );
  return (
    <section aria-labelledby="album-access-heading" className={className}>
      <h2 className={sectionHeadingClass} id="album-access-heading">
        Album access
      </h2>
      <p className="mt-2 text-xs text-muted">
        Give someone access across the album. Moment and item exceptions still
        apply.
      </p>
      <Form
        aria-busy={save.isPending}
        aria-label="Album access"
        className="mt-6"
        error={save.error}
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate(payload(draft), {
            onSuccess: () => review.reset(),
          });
        }}
      >
        <fieldset disabled={save.isPending}>
          {album.people.map((person) => (
            <div
              className="flex items-center gap-3 border-t border-border py-3"
              key={person.person_id}
            >
              <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-sm">
                <input
                  aria-label={`Album access for ${person.display_name}`}
                  checked={!!draft[person.person_id]}
                  className="size-4 cursor-pointer accent-primary"
                  onChange={(event) => {
                    const next = {
                      ...draft,
                      [person.person_id]: event.target.checked,
                    };
                    setDraft(next);
                    review.mutate(payload(next));
                  }}
                  type="checkbox"
                />
                <PersonAvatar person={person} />
                <span className="min-w-0">
                  <strong className="block truncate font-medium">
                    {person.display_name}
                  </strong>
                  <small className="block text-xs text-muted">
                    {person.accessible_entries} of {total} items accessible now
                    {person.moment_decisions + person.entry_decisions > 0 &&
                      `, ${countLabel(person.moment_decisions + person.entry_decisions, "exception", "exceptions")}`}
                  </small>
                </span>
              </label>
              <Button
                className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground"
                onClick={() => setRemoving(person.person_id)}
                variant="ghost"
              >
                Remove all access…
              </Button>
            </div>
          ))}
          <p className="mt-3 text-xs text-muted">
            Unchecking a person removes only Album-wide access. Their Moment and
            item decisions stay in place.
          </p>
          <VisibilityReview
            album={album}
            className="my-6"
            preview={dirty ? review.data : undefined}
          />
          <Button disabled={!dirty} type="submit">
            {save.isPending ? "Saving…" : "Save Album access"}
          </Button>
          {save.isSuccess && !dirty && (
            <p className="mt-4 text-sm text-muted" role="status">
              Album access saved.
            </p>
          )}
        </fieldset>
      </Form>
      {removingPerson && (
        <RemoveAllAccessDialog
          album={album}
          onClose={() => setRemoving(null)}
          person={removingPerson}
        />
      )}
    </section>
  );
}

export function RemoveAllAccessDialog({
  album,
  person,
  onClose,
}: {
  album: PrototypeAlbum;
  person: { person_id: string; display_name: string };
  onClose: () => void;
}) {
  const review = useAlbumReview<{ person_id: string }>(
    album.id,
    "access/remove-all/preview",
  );
  const remove = useAlbumAction<{ person_id: string }>(
    album.id,
    "access/remove-all",
  );
  const [reviewed, setReviewed] = useState(false);
  if (!reviewed && review.isIdle)
    review.mutate({ person_id: person.person_id });
  return (
    <Dialog
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
      open
    >
      <DialogContent>
        <DialogTitle className="pr-8">
          Remove all access for {person.display_name}?
        </DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Every Album, Moment, and item decision for {person.display_name} in
          this album is removed. Other people's access stays unchanged.
        </DialogDescription>
        <VisibilityReview
          album={album}
          className="my-6"
          preview={review.data}
        />
        <Form
          aria-busy={remove.isPending}
          aria-label={`Remove all access for ${person.display_name}`}
          error={remove.error}
          onSubmit={(event) => {
            event.preventDefault();
            setReviewed(true);
            remove.mutate(
              { person_id: person.person_id },
              { onSuccess: onClose },
            );
          }}
        >
          <fieldset className="flex gap-2" disabled={remove.isPending}>
            <Button type="submit">
              {remove.isPending ? "Removing…" : "Remove all access"}
            </Button>
            <Button onClick={onClose} variant="outline">
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Review & publish.

export function publicationBlockers(album: PrototypeAlbum) {
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  return [
    !album.title.trim() && "Add an album title.",
    total === 0 && "Import at least one photo or video.",
  ].filter((item): item is string => !!item);
}

export function PublishDialog({
  album,
  onClose,
}: {
  album: PrototypeAlbum;
  onClose: () => void;
}) {
  const publish = useAlbumAction(album.id, "publish");
  const unpublish = useAlbumAction(album.id, "unpublish");
  const pending = publish.isPending || unpublish.isPending;
  const blockers = publicationBlockers(album);
  const audience = album.people.filter(
    (person) => person.accessible_entries > 0,
  );
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  const suggestions = pendingSuggestions(album);
  const unlinked = album.moments.reduce(
    (count, moment) => count + unlinkedFaceCount(moment),
    0,
  );
  return (
    <Dialog onOpenChange={(open) => !open && !pending && onClose()} open>
      <DialogContent className="max-w-xl">
        <DialogTitle className="pr-8">
          {album.published ? "Published album" : "Ready to publish?"}
        </DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          {album.published
            ? "This album is available to the people below. Unpublishing hides it again and keeps every decision."
            : "Publishing makes this album available to the people below. It sends no notifications."}
        </DialogDescription>
        <section aria-label="Audience" className="mt-5 border-t border-border">
          {audience.length ? (
            audience.map((person) => (
              <div
                className="flex items-center gap-3 border-b border-border py-3 text-sm"
                key={person.person_id}
              >
                <PersonAvatar person={person} />
                <strong className="font-medium">{person.display_name}</strong>
                <span className="ml-auto text-xs text-muted">
                  {person.accessible_entries} of {total} items
                </span>
              </div>
            ))
          ) : (
            <p className="border-b border-border py-3 text-sm text-muted">
              No one has access yet. You can publish now and grant access later.
            </p>
          )}
        </section>
        <ul className="mt-5 space-y-2 text-sm">
          <li>
            {countLabel(total, "item", "items")} in{" "}
            {countLabel(album.moments.length, "Moment", "Moments")}
          </li>
          <li>No unfinished structural edits</li>
          {suggestions > 0 && (
            <li className="text-muted">
              {countLabel(
                suggestions,
                "access suggestion",
                "access suggestions",
              )}{" "}
              still waiting. Optional to review.
            </li>
          )}
          {unlinked > 0 && (
            <li className="text-muted">
              {countLabel(unlinked, "unlinked face", "unlinked faces")}.
              Optional to link.
            </li>
          )}
        </ul>
        {blockers.map((blocker) => (
          <p
            className="mt-4 text-sm text-destructive"
            key={blocker}
            role="alert"
          >
            {blocker}
          </p>
        ))}
        <Form
          aria-busy={pending}
          aria-label={album.published ? "Unpublish album" : "Publish album"}
          className="mt-6"
          error={publish.error ?? unpublish.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (album.published) unpublish.mutate({}, { onSuccess: onClose });
            else publish.mutate({}, { onSuccess: onClose });
          }}
        >
          <fieldset className="flex flex-wrap gap-2" disabled={pending}>
            <Button
              disabled={!album.published && blockers.length > 0}
              type="submit"
            >
              {pending
                ? album.published
                  ? "Unpublishing…"
                  : "Publishing…"
                : album.published
                  ? "Unpublish album"
                  : "Publish album"}
            </Button>
            <Button onClick={onClose} variant="outline">
              Keep editing
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}

export function PublishChecklist({ album }: { album: PrototypeAlbum }) {
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  const checks = [
    { ok: !!album.title.trim(), label: "Album has a title" },
    {
      ok: total > 0,
      label: `${countLabel(total, "item", "items")} assigned to ${countLabel(album.moments.length, "Moment", "Moments")}`,
    },
    { ok: true, label: "No unfinished structural edits" },
  ];
  return (
    <section
      aria-labelledby="before-publish"
      className="mt-9 border-t border-border pt-6"
    >
      <h3 className="font-heading text-xl" id="before-publish">
        Before you publish
      </h3>
      <ul className="mt-4 space-y-2 text-sm">
        {checks.map((check) => (
          <li className="flex items-center gap-3" key={check.label}>
            <span
              aria-hidden="true"
              className={cn(
                "inline-block size-2 rounded-full",
                check.ok ? "bg-primary" : "bg-destructive",
              )}
            />
            {check.label}
          </li>
        ))}
      </ul>
      <p className="mt-4 text-xs leading-relaxed text-muted">
        Review Moment access and preview as a person, then publish when you're
        ready. Suggestions and unlinked faces never block publication.
      </p>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Viewer preview: the approved viewer presentation inside the editor.

export function ViewerPreviewSection({
  album,
  className,
}: {
  album: PrototypeAlbum;
  className?: string;
}) {
  const { params, update, link } = useURLState();
  const personID =
    album.people.find((person) => person.person_id === params.get("person"))
      ?.person_id ??
    album.people[0]?.person_id ??
    "";
  const tab = params.get("tab") === "videos" ? "videos" : "photos";
  const preview = useViewerPreview(album.id, personID);
  const viewer = preview.data;
  const labelId = useId();
  return (
    <section aria-labelledby="viewer-preview-heading" className={className}>
      <h2 className="sr-only" id="viewer-preview-heading">
        Viewer preview
      </h2>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-3 rounded-md bg-surface px-4 py-3 text-xs">
        <span className="flex items-center gap-3">
          <span className="font-medium" id={labelId}>
            Preview as
          </span>
          <Combobox
            aria-labelledby={labelId}
            className="min-h-9 w-40"
            onChange={(value) => update({ person: value })}
            options={album.people.map((person) => ({
              value: person.person_id,
              label: person.display_name,
              leading: <PersonAvatar className="size-6" person={person} />,
            }))}
            placeholder="Choose a person"
            value={personID}
          />
        </span>
        <p className="text-muted">
          Read only. Downloads and account actions are off.
          {!album.published && " Showing the view after publication."}
        </p>
      </div>
      {preview.isPending && (
        <p className="mt-6 text-sm text-muted" role="status">
          Building the preview…
        </p>
      )}
      {preview.isError && (
        <p className="mt-6 text-sm text-destructive" role="alert">
          The preview could not be built.
        </p>
      )}
      {viewer && (
        <div className="mt-8">
          <div className="grid gap-8 min-[761px]:grid-cols-[minmax(0,1fr)_minmax(0,380px)] min-[761px]:items-center">
            <div>
              <h3 className={headingClass}>{viewer.title}</h3>
              {viewer.description && (
                <p className="mt-4 max-w-[560px] text-sm text-muted">
                  {viewer.description}
                </p>
              )}
              <p className="mt-5 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted">
                <span>
                  {dateRange(viewer.start_date, viewer.end_date) ||
                    "No accessible media"}
                </span>
                <span className="inline-flex items-center gap-1.5">
                  <Image
                    aria-hidden="true"
                    className="size-3.5"
                    strokeWidth={1.5}
                  />
                  {countLabel(viewer.photo_count, "photo", "photos")}
                </span>
                <span className="inline-flex items-center gap-1.5">
                  <SquarePlay
                    aria-hidden="true"
                    className="size-3.5"
                    strokeWidth={1.5}
                  />
                  {countLabel(viewer.video_count, "video", "videos")}
                </span>
              </p>
            </div>
            <AlbumImage
              alt="Album cover"
              className="w-full"
              fallback={`No cover is visible to ${viewer.display_name}`}
              src={viewer.cover_url}
            />
          </div>
          <nav
            aria-label="Media tabs"
            className="mt-8 flex border-b border-border"
          >
            {[
              {
                key: "photos",
                label: "Photos",
                count: viewer.photo_count,
                icon: Image,
              },
              {
                key: "videos",
                label: "Videos",
                count: viewer.video_count,
                icon: SquarePlay,
              },
            ].map((item) => (
              <Link
                aria-current={tab === item.key ? "page" : undefined}
                className={cn(
                  "-mb-px inline-flex items-center gap-2 border-b-2 px-4 py-3 text-sm",
                  tab === item.key
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted hover:text-foreground",
                )}
                key={item.key}
                to={link({ tab: item.key })}
              >
                <item.icon
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
                {item.label}
                <span className="rounded-sm bg-surface px-1.5 text-xs">
                  {item.count}
                </span>
              </Link>
            ))}
          </nav>
          <ViewerGallery
            kind={tab === "photos" ? "IMAGE" : "VIDEO"}
            viewer={viewer}
          />
        </div>
      )}
    </section>
  );
}

function ViewerGallery({
  viewer,
  kind,
}: {
  viewer: ViewerAlbum;
  kind: "IMAGE" | "VIDEO";
}) {
  const days = viewer.days
    .map((day) => ({
      ...day,
      entries: day.entries.filter((entry) => entry.kind === kind),
    }))
    .filter((day) => day.entries.length > 0);
  if (days.length === 0)
    return (
      <p className="py-8 text-sm text-muted">
        {kind === "IMAGE"
          ? `No photos are shared with ${viewer.display_name} yet.`
          : `No videos are shared with ${viewer.display_name} yet.`}
      </p>
    );
  return (
    <div className="mt-2">
      {days.map((day) => (
        <section
          aria-label={dayLabel(day.date, true)}
          className="pt-8"
          key={day.date}
        >
          <h4 className="font-heading text-[27px]/[1.2]">
            {dayLabel(day.date, true)}{" "}
            <span className="ml-2 font-sans text-xs text-muted">
              {countLabel(
                day.entries.length,
                kind === "IMAGE" ? "photo" : "video",
                kind === "IMAGE" ? "photos" : "videos",
              )}
            </span>
          </h4>
          <ul className="mt-4 grid grid-cols-2 gap-2 min-[761px]:grid-cols-3">
            {day.entries.map((entry) => (
              <li className="relative" key={entry.id}>
                <AlbumImage
                  alt={entry.filename}
                  className="w-full"
                  fallback="Unavailable"
                  src={entry.thumbnail_url}
                />
                {entry.kind === "VIDEO" && (
                  <span className="absolute right-2 bottom-2 rounded-sm bg-black/70 p-1 text-white">
                    <SquarePlay aria-hidden="true" className="size-4" />
                  </span>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Command bar pieces.

export function PublicationStatus({ album }: { album: PrototypeAlbum }) {
  return (
    <span
      className={cn(album.published ? "text-accent-foreground" : "text-muted")}
    >
      {album.published ? "Published" : "Unpublished"}
    </span>
  );
}

export function useMomentByParam(album: PrototypeAlbum) {
  const { params } = useURLState();
  return (
    album.moments.find((moment) => moment.id === params.get("moment")) ??
    album.moments[0]
  );
}

export function momentCover(moment: Moment) {
  return moment.entries.find((entry) => entry.id === moment.cover_entry_id);
}
