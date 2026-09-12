import { useLayoutEffect, useRef, useState } from "react";

import { useSetAccessRules } from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { AccessPerson, Decision } from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Form } from "../people/form-fields";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";

export function AccessRules({
  albumID,
  target,
  targetID,
  people,
  inheritedAllows,
  onClose,
}: {
  albumID: string;
  target: "moments" | "entries";
  targetID: string;
  people: AccessPerson[];
  inheritedAllows: string[];
  onClose: () => void;
}) {
  const save = useSetAccessRules(albumID, target, targetID);
  const [draft, setDraft] = useState<Record<string, Decision>>({});
  const [discardOpen, setDiscardOpen] = useState(false);
  const dirty = Object.keys(draft).length > 0;
  useUnsavedChanges(dirty || save.isPending, true);
  const errors = fieldErrors(save.error);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, []);
  function close() {
    if (save.isPending) return;
    if (dirty) setDiscardOpen(true);
    else onClose();
  }
  return (
    <>
      <Dialog onOpenChange={(open) => !open && close()} open>
        <DialogContent
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (returnFocusRef.current?.isConnected)
              returnFocusRef.current.focus();
          }}
        >
          <DialogTitle>Rules & exceptions</DialogTitle>
          <DialogDescription className="mt-2 text-sm text-muted">
            {target === "moments"
              ? "Moment decisions override Album access. Item exceptions still apply."
              : "Item decisions override Moment and Album access."}
          </DialogDescription>
          <Form
            aria-busy={save.isPending}
            aria-label="Rules & exceptions"
            className="mt-5"
            error={save.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (save.isPending) return;
              save.mutate(
                {
                  decisions: Object.entries(draft).map(
                    ([person_id, decision]) => ({ person_id, decision }),
                  ),
                },
                { onSuccess: onClose },
              );
            }}
          >
            <fieldset disabled={save.isPending}>
              {people.map((person) => (
                <div
                  className="grid gap-2 border-t border-border py-3 sm:grid-cols-2 sm:items-center"
                  key={person.person_id}
                >
                  <div>
                    <p className="text-sm font-medium">{person.display_name}</p>
                    <p className="text-xs text-muted">
                      {person.detected ? "Detected here" : "Not detected here"}
                    </p>
                  </div>
                  <Combobox
                    aria-describedby={
                      errors.decisions ? "rules-error" : undefined
                    }
                    aria-invalid={!!errors.decisions}
                    aria-label={`Access for ${person.display_name}`}
                    disabled={save.isPending}
                    onChange={(decision) => {
                      save.reset();
                      setDraft((current) => ({
                        ...current,
                        [person.person_id]: decision,
                      }));
                    }}
                    options={[
                      { value: "allow", label: "Allow" },
                      { value: "deny", label: "Exclude" },
                      {
                        value: "inherit",
                        label: inheritedAllows.includes(person.person_id)
                          ? "Inherit: allowed by broader access"
                          : "Inherit: no access",
                      },
                    ]}
                    value={
                      draft[person.person_id] ?? (person.decision || "inherit")
                    }
                  />
                </div>
              ))}
              {errors.decisions && (
                <p className="text-xs text-destructive" id="rules-error">
                  {errors.decisions}
                </p>
              )}
              <div className="mt-5 flex gap-2">
                <Button
                  disabled={Object.keys(draft).length === 0}
                  type="submit"
                >
                  {save.isPending ? "Saving…" : "Save access"}
                </Button>
                <Button onClick={close} type="button" variant="outline">
                  Cancel
                </Button>
              </div>
            </fieldset>
          </Form>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description="Your saved access decisions stay unchanged."
        onConfirm={onClose}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Discard access changes?"
      />
    </>
  );
}
