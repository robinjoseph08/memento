import { ArrowLeft } from "lucide-react";
import { useState } from "react";

import {
  useAddMomentSuggestions,
  useSetMomentAccess,
  useUndoMomentAccess,
} from "../../hooks/queries/albums";
import type {
  Entry,
  Moment,
  UndoMomentAccessRequest,
} from "../../types/generated/publishing";
import { Failure } from "../people/form-fields";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";
import { FaceManagement } from "./face-management";

function initials(name: string) {
  return name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export function MomentAccessInspector({
  albumID,
  moment,
  entry,
  undo,
  onUndo,
  onBack,
  refreshError,
  refreshing,
}: {
  albumID: string;
  moment: Moment;
  entry?: Entry;
  undo: UndoMomentAccessRequest | null;
  onUndo: (undo: UndoMomentAccessRequest | null) => void;
  onBack: () => void;
  refreshError: Error | null;
  refreshing: boolean;
}) {
  const change = useSetMomentAccess(albumID, moment.id);
  const [optimistic, setOptimistic] = useState<
    Record<string, "allow" | "deny">
  >({});
  const addSuggestions = useAddMomentSuggestions(albumID, moment.id);
  const undoChange = useUndoMomentAccess(albumID, moment.id);
  const pending =
    change.isPending || addSuggestions.isPending || undoChange.isPending;
  const allowed = moment.access.people.filter(
    (person) => person.decision === "allow",
  );
  const suggested = moment.access.people.filter((person) => person.suggested);
  const excluded = moment.access.people.filter(
    (person) => person.decision === "deny",
  );
  const others = moment.access.people.filter(
    (person) => !person.decision && !person.suggested,
  );
  const mutationError =
    change.error ?? addSuggestions.error ?? undoChange.error;

  function personRow(person: (typeof moment.access.people)[number]) {
    const pendingDecision = optimistic[person.person_id] ?? person.decision;
    const detail = person.suggested
      ? `Detected in ${person.supporting_entries} ${person.supporting_entries === 1 ? "item" : "items"}, not shared yet`
      : person.decision === "deny"
        ? person.detected
          ? "Detected here, explicitly excluded"
          : "Saved exclusion, no current detection"
        : person.detected
          ? `Allowed, detected in ${person.supporting_entries} ${person.supporting_entries === 1 ? "item" : "items"}`
          : "Saved access, no current detection";
    return (
      <label
        className="flex cursor-pointer items-center gap-3 border-t border-border py-3 text-sm"
        key={person.person_id}
      >
        <input
          aria-label={`Allow ${person.display_name} for this Moment`}
          checked={pendingDecision === "allow"}
          className="size-4 cursor-pointer accent-primary"
          disabled={pending}
          onChange={(event) => {
            const decision = event.target.checked ? "allow" : "deny";
            setOptimistic((current) => ({
              ...current,
              [person.person_id]: decision,
            }));
            change.mutate(
              { person_id: person.person_id, decision },
              {
                onSuccess: (result) => onUndo(result.undo),
                onSettled: () =>
                  setOptimistic((current) => {
                    const next = { ...current };
                    delete next[person.person_id];
                    return next;
                  }),
              },
            );
          }}
          type="checkbox"
        />
        <Avatar>
          {person.avatar_url && <AvatarImage alt="" src={person.avatar_url} />}
          <AvatarFallback>{initials(person.display_name)}</AvatarFallback>
        </Avatar>
        <span className="min-w-0">
          <strong className="block truncate font-medium">
            {person.display_name}
          </strong>
          <small className="mt-0.5 block text-xs leading-relaxed text-muted">
            {detail}
          </small>
        </span>
      </label>
    );
  }

  return (
    <div className="min-w-0">
      {entry ? (
        <>
          <Button className="mb-4 -ml-3" onClick={onBack} variant="ghost">
            <ArrowLeft aria-hidden="true" className="size-4" />
            Moment access
          </Button>
          <AlbumImage
            alt={entry.filename}
            className="mb-4 max-h-44 w-full object-contain"
            fallback="No preview available"
            src={entry.available ? entry.thumbnail_url : ""}
          />
          <p className="mb-5 truncate text-sm text-muted">{entry.filename}</p>
        </>
      ) : (
        <p className="text-xs text-muted">Moment access</p>
      )}
      <h2 className="mt-1 font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]">
        {moment.label}
      </h2>
      <div className="mt-4 flex items-center justify-between gap-3">
        <p className="text-xs text-muted">Changes save immediately.</p>
        <Button
          className="h-auto min-h-0 px-0 py-1 text-xs"
          disabled={!undo || pending}
          onClick={() =>
            undo &&
            undoChange.mutate(undo, {
              onSuccess: () => onUndo(null),
            })
          }
          variant="ghost"
        >
          {undoChange.isPending ? "Undoing…" : "Undo"}
        </Button>
      </div>
      <Failure error={mutationError} />
      <Failure error={refreshError} />
      {refreshing && (
        <p className="mt-4 text-xs text-muted" role="status">
          Refreshing faces from Immich…
        </p>
      )}
      {moment.access.refreshed_at && !refreshing && (
        <p className="mt-4 text-xs text-muted">
          Faces refreshed{" "}
          <time dateTime={moment.access.refreshed_at}>
            {new Date(moment.access.refreshed_at).toLocaleString()}
          </time>
        </p>
      )}
      {moment.access.refresh_error && (
        <p className="mt-4 text-xs text-destructive" role="alert">
          {moment.access.refresh_error}
        </p>
      )}
      <form
        aria-label="Quick Moment access"
        className="mt-7"
        onSubmit={(event) => event.preventDefault()}
      >
        <fieldset disabled={pending}>
          <legend className="mb-1 text-sm font-medium">
            Allowed{" "}
            <span className="ml-1 text-xs text-muted">{allowed.length}</span>
          </legend>
          {allowed.length > 0 ? (
            allowed.map(personRow)
          ) : (
            <p className="border-t border-border py-4 text-xs text-muted">
              No one is allowed at this Moment yet.
            </p>
          )}
          {suggested.length > 0 && (
            <section aria-labelledby="suggested-access" className="mt-6">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-medium" id="suggested-access">
                  Suggested{" "}
                  <span className="ml-1 text-xs text-muted">
                    {suggested.length}
                  </span>
                </h3>
                <Button
                  className="h-auto min-h-0 px-0 py-1 text-xs"
                  onClick={() =>
                    addSuggestions.mutate(undefined, {
                      onSuccess: (result) => onUndo(result.undo),
                    })
                  }
                  type="button"
                  variant="ghost"
                >
                  Add all suggested
                </Button>
              </div>
              {suggested.map(personRow)}
            </section>
          )}
          {excluded.length > 0 && (
            <section aria-labelledby="excluded-access" className="mt-6">
              <h3 className="mb-1 text-sm font-medium" id="excluded-access">
                Excluded{" "}
                <span className="ml-1 text-xs text-muted">
                  {excluded.length}
                </span>
              </h3>
              {excluded.map(personRow)}
            </section>
          )}
          {others.length > 0 && (
            <details className="mt-5">
              <summary className="cursor-pointer py-2 text-xs text-accent-foreground">
                Add someone else
              </summary>
              {others.map(personRow)}
            </details>
          )}
        </fieldset>
      </form>
      <FaceManagement faces={moment.access.faces} />
      <details className="mt-7 border-t border-border pt-4 text-xs text-muted">
        <summary className="cursor-pointer py-1 text-foreground">
          How access works
        </summary>
        <p className="mt-3 leading-relaxed">
          Checking a Person explicitly allows this Moment. Unchecking explicitly
          excludes them. Face detections only suggest access.
        </p>
      </details>
    </div>
  );
}
