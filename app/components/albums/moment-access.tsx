import { RefreshCw } from "lucide-react";
import { useState } from "react";

import {
  useAddMomentSuggestions,
  useSetMomentAccess,
  useUndoMomentAccess,
} from "../../hooks/queries/albums";
import { initials } from "../../lib/initials";
import type {
  AccessPerson,
  Moment,
  UndoMomentAccessRequest,
} from "../../types/generated/publishing";
import { Failure } from "../people/form-fields";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";
import { UnlinkedFaces } from "./face-management";

// Most-seen people first, then alphabetical, so the busiest rows lead.
function byPresence(left: AccessPerson, right: AccessPerson) {
  if (left.supporting_entries !== right.supporting_entries)
    return right.supporting_entries - left.supporting_entries;
  return left.display_name.localeCompare(right.display_name);
}

function detectionDetail(person: AccessPerson) {
  if (!person.detected) return "Not seen in this Moment";
  return `Seen in ${person.supporting_entries} ${person.supporting_entries === 1 ? "item" : "items"}`;
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
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="mt-6 first:mt-0">
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

export function MomentAccessInspector({
  albumID,
  moment,
  undo,
  onUndo,
  refreshError,
  refreshing,
  onRefresh,
}: {
  albumID: string;
  moment: Moment;
  undo: UndoMomentAccessRequest | null;
  onUndo: (undo: UndoMomentAccessRequest | null) => void;
  refreshError: Error | null;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const change = useSetMomentAccess(albumID, moment.id);
  const [optimistic, setOptimistic] = useState<
    Record<string, "allow" | "deny">
  >({});
  const addSuggestions = useAddMomentSuggestions(albumID, moment.id);
  const undoChange = useUndoMomentAccess(albumID, moment.id);
  const pending =
    change.isPending || addSuggestions.isPending || undoChange.isPending;
  const people = [...moment.access.people].sort(byPresence);
  const allowed = people.filter((person) => person.decision === "allow");
  const suggested = people.filter((person) => person.suggested);
  const excluded = people.filter((person) => person.decision === "deny");
  const others = people.filter(
    (person) => !person.decision && !person.suggested,
  );
  const mutationError =
    change.error ?? addSuggestions.error ?? undoChange.error;
  const refreshedAt = moment.access.refreshed_at
    ? new Date(moment.access.refreshed_at).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "";

  function personRow(person: AccessPerson) {
    const pendingDecision = optimistic[person.person_id] ?? person.decision;
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
          <small className="block text-xs text-muted">
            {detectionDetail(person)}
          </small>
        </span>
      </label>
    );
  }

  return (
    <div className="min-w-0">
      <p className="text-xs text-muted">Moment access</p>
      <h2 className="mt-1 font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]">
        {moment.label}
      </h2>
      <div className="mt-3 flex items-center justify-between gap-3">
        <p className="text-xs text-muted">Changes save immediately.</p>
        <Button
          className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
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
      <form
        aria-label="Quick Moment access"
        className="mt-6"
        onSubmit={(event) => event.preventDefault()}
      >
        <fieldset disabled={pending}>
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
          {suggested.length > 0 && (
            <AccessGroup
              action={
                <Button
                  className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
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
              }
              count={suggested.length}
              id="suggested-access"
              title="Suggested"
            >
              {suggested.map(personRow)}
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
            <details className="mt-4">
              <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
                Add someone else
              </summary>
              {others.map(personRow)}
            </details>
          )}
        </fieldset>
      </form>
      <UnlinkedFaces faces={moment.access.faces} />
      <div className="mt-6 border-t border-border pt-4 text-xs text-muted">
        <div className="flex items-center justify-between gap-3">
          {refreshing ? (
            <p role="status">Checking Immich for faces…</p>
          ) : refreshError ? (
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
                  disabled={refreshing}
                  onClick={onRefresh}
                  size="sm"
                  type="button"
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
        <details className="mt-3">
          <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-1 text-foreground hover:bg-surface">
            How access works
          </summary>
          <p className="mt-2 leading-relaxed">
            Checking a person allows this Moment. Unchecking excludes them.
            Faces Immich recognized only suggest access; nothing changes until
            you choose.
          </p>
        </details>
      </div>
    </div>
  );
}
