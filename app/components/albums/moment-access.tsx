import { RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";

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
import { countLabel } from "./moment-labels";

// Most-seen people first, then alphabetical, so the busiest rows lead.
function byPresence(left: AccessPerson, right: AccessPerson) {
  if (left.supporting_entries !== right.supporting_entries)
    return right.supporting_entries - left.supporting_entries;
  return left.display_name.localeCompare(right.display_name);
}

function detectionDetail(person: AccessPerson) {
  if (person.suggested) return "Detected here, not shared yet";
  if (!person.detected) return "Not seen in this Moment";
  return `Seen in ${countLabel(person.supporting_entries, "item", "items")}`;
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

// The access strip above a Moment's media: Allowed and Suggested side by
// side, Excluded and Add someone else beneath, unlinked faces collapsed to a
// count, and the faces-checked line along the bottom. Checking or unchecking
// a person saves immediately with one-step Undo.
export function MomentAccessStrip({
  albumID,
  moment,
  refreshError,
  refreshing,
  onRefresh,
}: {
  albumID: string;
  moment: Moment;
  refreshError: Error | null;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const change = useSetMomentAccess(albumID, moment.id);
  const [optimistic, setOptimistic] = useState<
    Record<string, "allow" | "deny">
  >({});
  const [undo, setUndo] = useState<UndoMomentAccessRequest | null>(null);
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
  const unlinked = moment.access.faces.filter(
    (face) => !face.person_id && !face.ignored,
  ).length;
  const ignored = moment.access.faces.filter(
    (face) => !face.person_id && face.ignored,
  ).length;
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
                onSuccess: (result) => setUndo(result.undo),
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
    <section
      aria-label="Moment access"
      className="min-w-0 rounded-md border border-border p-4"
    >
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted">Changes save immediately.</p>
        <Button
          className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
          disabled={!undo?.changes.length || pending}
          onClick={() =>
            undo && undoChange.mutate(undo, { onSuccess: () => setUndo(null) })
          }
          variant="ghost"
        >
          {undoChange.isPending ? "Undoing…" : "Undo"}
        </Button>
      </div>
      <Failure error={mutationError} />
      <form
        aria-label="Quick Moment access"
        className="mt-3"
        onSubmit={(event) => event.preventDefault()}
      >
        <fieldset
          className="grid gap-x-8 gap-y-5 min-[1000px]:grid-cols-2"
          disabled={pending}
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
          <AccessGroup
            action={
              suggested.length > 0 && (
                <Button
                  className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
                  onClick={() =>
                    addSuggestions.mutate(undefined, {
                      onSuccess: (result) => setUndo(result.undo),
                    })
                  }
                  type="button"
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
            <details className="min-[1000px]:col-span-2">
              <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
                Add someone else
              </summary>
              {others.map(personRow)}
            </details>
          )}
        </fieldset>
      </form>
      {/* Ignored faces stay reachable here so a mistaken Ignore can be undone
          by linking the face after all. */}
      {(unlinked > 0 || ignored > 0) && (
        <details className="mt-3">
          <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-1 text-xs text-accent-foreground hover:bg-surface">
            {unlinked > 0
              ? `${countLabel(unlinked, "unlinked face", "unlinked faces")} to link`
              : countLabel(ignored, "ignored face", "ignored faces")}
          </summary>
          <UnlinkedFaces faces={moment.access.faces} />
        </details>
      )}
      <div className="mt-4 flex flex-wrap items-start gap-x-6 gap-y-2 border-t border-border pt-4 text-xs text-muted">
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
        <details className="min-w-0 flex-1 basis-60">
          {/* Matches the refresh button's height so the collapsed row reads as
              one centered line while the open one stays anchored. */}
          <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-[7px] text-foreground hover:bg-surface">
            How access works
          </summary>
          <p className="mt-2 leading-relaxed">
            Checking a person allows this Moment. Unchecking excludes them.
            Faces Immich recognized only suggest access; nothing changes until
            you choose.
          </p>
        </details>
      </div>
    </section>
  );
}
