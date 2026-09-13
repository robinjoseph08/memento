import { useEffect, useEffectEvent, useId, useState } from "react";

import { useSetAccessRules } from "../../hooks/queries/albums";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  AccessPerson,
  AlbumDetail,
  Decision,
  Entry,
  Moment,
} from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { FieldError, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { accessDetail, byPresence } from "./access-labels";
import { AlbumImage } from "./album-image";
import { PersonAvatar } from "./person-avatar";

// Detailed allow, deny, and inherit editing for one Moment or one item. It
// opens on saved decisions only: detected people are listed, never preselected.
export function RulesDialog({
  album,
  moment,
  entry,
  onClose,
}: {
  album: AlbumDetail;
  moment: Moment;
  entry?: Entry;
  onClose: () => void;
}) {
  const save = useSetAccessRules(
    album.id,
    entry ? "entries" : "moments",
    entry ? entry.id : moment.id,
  );
  const people = [...moment.access.people].sort(byPresence);
  const saved = (person: AccessPerson): Decision => {
    const current = entry ? entry.decisions[person.person_id] : person.decision;
    return current === "allow" || current === "deny" ? current : "inherit";
  };
  const [draft, setDraft] = useState<Record<string, Decision>>({});
  const value = (person: AccessPerson) =>
    draft[person.person_id] ?? saved(person);
  const changed = people.filter((person) => value(person) !== saved(person));
  const dirty = save.isPending || changed.length > 0;
  useUnsavedChanges(dirty, true);
  // Close only once the dialog is no longer dirty: an item dialog closes by
  // clearing ?entry, which the unsaved-changes guard would otherwise block.
  const [discarded, setDiscarded] = useState(false);
  const closeSettled = useEffectEvent(onClose);
  useEffect(() => {
    if (save.isSuccess || discarded) closeSettled();
  }, [save.isSuccess, discarded]);
  const [discardOpen, setDiscardOpen] = useState(false);
  const returnFocus = useReturnFocus();
  const errors = fieldErrors(save.error);
  const errorId = useId();
  const albumAllowed = (person: AccessPerson) =>
    album.access.some(
      (item) =>
        item.person_id === person.person_id && item.decision === "allow",
    );
  const inheritedLabel = (person: AccessPerson) => {
    if (!entry)
      return albumAllowed(person)
        ? "Inherit: allowed by Album access"
        : "Inherit: no access";
    if (person.decision === "allow") return "Inherit: allowed by this Moment";
    if (person.decision === "deny") return "Inherit: excluded by this Moment";
    return albumAllowed(person)
      ? "Inherit: allowed by Album access"
      : "Inherit: no access";
  };
  function changeOpen(next: boolean) {
    if (next || save.isPending) return;
    if (dirty) setDiscardOpen(true);
    else onClose();
  }
  return (
    <>
      <Dialog onOpenChange={changeOpen} open>
        <DialogContent className="max-w-xl" onCloseAutoFocus={returnFocus}>
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
              if (save.isPending) return;
              if (changed.length === 0) {
                onClose();
                return;
              }
              save.mutate({
                decisions: changed.map((person) => ({
                  person_id: person.person_id,
                  decision: value(person),
                })),
              });
            }}
          >
            <fieldset disabled={save.isPending}>
              {people.map((person) => (
                <RuleRow
                  describedBy={errors.decisions ? errorId : undefined}
                  inheritedLabel={inheritedLabel(person)}
                  key={person.person_id}
                  onChange={(decision) => {
                    save.reset();
                    setDraft((current) => ({
                      ...current,
                      [person.person_id]: decision,
                    }));
                  }}
                  person={person}
                  scopeNote={entry ? "" : accessDetail(person)}
                  value={value(person)}
                />
              ))}
              {people.length === 0 && (
                <p className="border-t border-border py-3 text-sm text-muted">
                  No active viewers to grant access to.
                </p>
              )}
              <FieldError error={errors.decisions} id={errorId} />
              <div className="mt-6 flex flex-wrap gap-2">
                <Button type="submit">
                  {save.isPending ? "Saving…" : "Save access"}
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
        description="Saved access stays as it is."
        onConfirm={() => {
          setDraft({});
          setDiscarded(true);
        }}
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
  describedBy,
}: {
  person: AccessPerson;
  value: Decision;
  onChange: (decision: Decision) => void;
  inheritedLabel: string;
  scopeNote: string;
  describedBy?: string;
}) {
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-1 border-t border-border py-3 min-[601px]:grid-cols-[auto_minmax(0,1fr)_200px]">
      <PersonAvatar person={person} />
      <span className="min-w-0">
        <strong className="block truncate text-sm font-medium">
          {person.display_name}
        </strong>
        {scopeNote && (
          <small className="block text-xs text-muted">{scopeNote}</small>
        )}
      </span>
      <div className="col-span-2 min-[601px]:col-span-1">
        <Combobox
          aria-describedby={describedBy}
          aria-invalid={!!describedBy}
          aria-label={`Access for ${person.display_name}`}
          onChange={(next) => onChange(next as Decision)}
          options={[
            { value: "inherit", label: inheritedLabel },
            { value: "allow", label: "Allow" },
            { value: "deny", label: "Deny" },
          ]}
          value={value}
        />
      </div>
    </div>
  );
}
