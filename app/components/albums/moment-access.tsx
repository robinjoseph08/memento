import { RefreshCw } from "lucide-react";
import { useId, useState, type ReactNode } from "react";

import { useSetAccessRules } from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  AccessPerson,
  AlbumDetail,
  Moment,
} from "../../types/generated/publishing";
import { FieldError, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";
import { accessDetail, byPresence } from "./access-labels";
import { RulesDialog } from "./access-rules";
import { UnlinkedFaces } from "./face-management";
import { countLabel } from "./moment-labels";
import { PersonAvatar } from "./person-avatar";

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
// count, and the faces-checked line along the bottom. Checkboxes edit a draft
// that an explicit Save writes, like Album details and Album access.
export function MomentAccessStrip({
  album,
  moment,
  refreshError,
  refreshing,
  onRefresh,
}: {
  album: AlbumDetail;
  moment: Moment;
  refreshError: Error | null;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const save = useSetAccessRules(album.id, "moments", moment.id);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [draft, setDraft] = useState<Record<string, boolean>>({});
  const people = [...moment.access.people].sort(byPresence);
  const checked = (person: AccessPerson) =>
    draft[person.person_id] ?? person.effective;
  const changed = people.filter(
    (person) => checked(person) !== person.effective,
  );
  const dirty = save.isPending || changed.length > 0;
  useUnsavedChanges(dirty, true);
  const errors = fieldErrors(save.error);
  const errorId = useId();
  const allowed = people.filter((person) => person.effective);
  const suggested = people.filter((person) => person.suggested);
  const excluded = people.filter((person) => person.decision === "deny");
  const others = people.filter(
    (person) => !person.effective && !person.decision && !person.suggested,
  );
  const unlinked = moment.access.faces.filter(
    (face) => !face.person_id && !face.ignored,
  ).length;
  const ignored = moment.access.faces.filter(
    (face) => !face.person_id && face.ignored,
  ).length;
  const refreshedAt = moment.access.refreshed_at
    ? new Date(moment.access.refreshed_at).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "";
  function toggle(person: AccessPerson, next: boolean) {
    save.reset();
    setDraft((current) => ({ ...current, [person.person_id]: next }));
  }

  function personRow(person: AccessPerson) {
    return (
      <label
        className="flex cursor-pointer items-center gap-3 border-t border-border py-3 text-sm"
        key={person.person_id}
      >
        <input
          aria-label={`Allow ${person.display_name} for this Moment`}
          checked={checked(person)}
          className="size-4 cursor-pointer accent-primary"
          onChange={(event) => toggle(person, event.target.checked)}
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
  }

  return (
    <section
      aria-label="Moment access"
      className="min-w-0 rounded-md border border-border p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-muted">
          Check who can see this Moment, then save.
        </p>
        <Button
          className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground"
          disabled={save.isPending}
          onClick={() => setRulesOpen(true)}
          type="button"
          variant="ghost"
        >
          Rules & exceptions
        </Button>
      </div>
      {rulesOpen && (
        <RulesDialog
          album={album}
          moment={moment}
          onClose={() => setRulesOpen(false)}
        />
      )}
      <Form
        aria-busy={save.isPending}
        aria-label="Moment access"
        className="mt-3"
        error={save.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (save.isPending || changed.length === 0) return;
          // Someone with Album access needs an explicit exclusion to lose
          // this Moment and only inherits again to regain it; everyone else
          // gets or loses a Moment allow.
          save.mutate(
            {
              decisions: changed.map((person) => ({
                person_id: person.person_id,
                decision: checked(person)
                  ? person.inherited
                    ? "inherit"
                    : "allow"
                  : person.inherited
                    ? "deny"
                    : "inherit",
              })),
            },
            { onSuccess: () => setDraft({}) },
          );
        }}
      >
        <fieldset
          aria-describedby={errors.decisions ? errorId : undefined}
          aria-invalid={!!errors.decisions}
          className="grid gap-x-8 gap-y-5 min-[1000px]:grid-cols-2"
          disabled={save.isPending}
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
              suggested.some((person) => !checked(person)) && (
                <Button
                  className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
                  onClick={() => {
                    save.reset();
                    setDraft((current) => ({
                      ...current,
                      ...Object.fromEntries(
                        suggested.map((person) => [person.person_id, true]),
                      ),
                    }));
                  }}
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
        <FieldError error={errors.decisions} id={errorId} />
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button disabled={!dirty || save.isPending} size="sm" type="submit">
            {save.isPending ? "Saving…" : "Save Moment access"}
          </Button>
          {save.isSuccess && !dirty && (
            <p className="text-xs text-muted" role="status">
              Moment access saved.
            </p>
          )}
        </div>
      </Form>
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
            Checking a person allows this Moment. Unchecking excludes them, even
            when they have Album access. Item exceptions still win. Faces Immich
            recognized only suggest access; nothing changes until you save.
          </p>
        </details>
      </div>
    </section>
  );
}
