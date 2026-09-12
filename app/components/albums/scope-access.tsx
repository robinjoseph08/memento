import { useState, type ReactNode } from "react";

import {
  useSetScopeAccess,
  useUndoScopeAccess,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { initials } from "../../lib/initials";
import type {
  AccessPerson,
  UndoMomentAccessRequest,
} from "../../types/generated/publishing";
import { Form } from "../people/form-fields";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";

export function ScopeAccess({
  albumID,
  entryID,
  inheritedAllows = [],
  people,
  total,
  personAction,
}: {
  albumID: string;
  entryID?: string;
  inheritedAllows?: string[];
  people: AccessPerson[];
  total: number;
  personAction?: (person: AccessPerson) => ReactNode;
}) {
  const change = useSetScopeAccess(albumID, entryID);
  const undoChange = useUndoScopeAccess(albumID, entryID);
  const [undo, setUndo] = useState<UndoMomentAccessRequest | null>(null);
  const pending = change.isPending || undoChange.isPending;
  const errors = fieldErrors(change.error);
  const fieldError = errors.person_id ?? errors.decision;
  useUnsavedChanges(pending, true);
  return (
    <Form
      aria-busy={pending}
      aria-label={entryID ? "Quick item access" : "Album access"}
      error={change.error ?? undoChange.error}
      onSubmit={(event) => event.preventDefault()}
    >
      <div className="my-4 flex items-center justify-between gap-3">
        <p className="text-xs text-muted">Changes save immediately.</p>
        <Button
          disabled={pending || !undo?.changes.length}
          onClick={() =>
            undo && undoChange.mutate(undo, { onSuccess: () => setUndo(null) })
          }
          size="sm"
          type="button"
          variant="ghost"
        >
          {undoChange.isPending ? "Undoing…" : "Undo"}
        </Button>
      </div>
      <fieldset disabled={pending}>
        {people.map((person) => (
          <div
            className="flex flex-wrap items-center justify-between gap-3 border-t border-border py-3"
            key={person.person_id}
          >
            <label className="flex cursor-pointer items-center gap-3 text-sm">
              <input
                aria-describedby={
                  change.variables?.person_id === person.person_id && fieldError
                    ? `access-error-${person.person_id}`
                    : undefined
                }
                aria-invalid={
                  change.variables?.person_id === person.person_id &&
                  !!fieldError
                }
                aria-label={`Allow ${person.display_name} for this ${entryID ? "item" : "Album"}`}
                checked={
                  entryID ? person.effective : person.decision === "allow"
                }
                className="size-4 cursor-pointer accent-primary"
                onChange={(event) => {
                  change.reset();
                  undoChange.reset();
                  change.mutate(
                    {
                      person_id: person.person_id,
                      decision: event.target.checked
                        ? "allow"
                        : entryID && inheritedAllows.includes(person.person_id)
                          ? "deny"
                          : "inherit",
                    },
                    { onSuccess: (result) => setUndo(result.undo) },
                  );
                }}
                type="checkbox"
              />
              <Avatar>
                {person.avatar_url && (
                  <AvatarImage alt="" src={person.avatar_url} />
                )}
                <AvatarFallback>{initials(person.display_name)}</AvatarFallback>
              </Avatar>
              <span className="min-w-0">
                <strong className="block font-medium">
                  {person.display_name}
                </strong>
                <small className="block text-xs text-muted">
                  {person.decision === "allow"
                    ? "Explicit allow"
                    : person.decision === "deny"
                      ? "Excluded"
                      : entryID && person.effective
                        ? "Inherited allow"
                        : person.effective
                          ? "Narrower access only"
                          : person.suggested
                            ? "Suggested"
                            : "No access"}
                </small>
                <small className="block text-xs text-muted">
                  {person.accessible_count} of {total} items accessible,{" "}
                  {person.excluded_count} excluded
                </small>
                {change.variables?.person_id === person.person_id &&
                  fieldError && (
                    <small
                      className="block text-xs text-destructive"
                      id={`access-error-${person.person_id}`}
                    >
                      {fieldError}
                    </small>
                  )}
              </span>
            </label>
            {personAction?.(person)}
          </div>
        ))}
      </fieldset>
      {people.length === 0 && (
        <p className="text-sm text-muted">
          No active viewers to grant access to.
        </p>
      )}
      {pending && (
        <p className="mt-3 text-xs text-muted" role="status">
          Saving access…
        </p>
      )}
    </Form>
  );
}
