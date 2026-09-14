import { useEffect, useEffectEvent, useId, useState } from "react";

import {
  useExcludeEntries,
  usePreviewExclude,
  useSetAccessRules,
} from "../../hooks/queries/albums";
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
import { VideoDetails } from "./video-details";

// Detailed allow, deny, and inherit editing for one Moment or one item. It
// opens on saved decisions only: detected people are listed, never preselected.
// A video item also carries its title and chapters above the access rules, so
// one dialog manages everything about the item.
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
  const video = entry?.kind === "VIDEO";
  const [videoDirty, setVideoDirty] = useState(false);
  const dirty = save.isPending || changed.length > 0 || videoDirty;
  useUnsavedChanges(dirty, true);
  // A save or a discard asks the dialog to close once nothing is unsaved: an
  // item dialog closes by clearing ?entry, which the unsaved-changes guard
  // would otherwise block, and a save reports success one render before the
  // refreshed Album arrives. Any later edit withdraws the request, so a
  // dialog left open with other unsaved work never closes on its own.
  const [closeWhenSettled, setCloseWhenSettled] = useState(false);
  // Counting discards remounts the video section so its title resets.
  const [discards, setDiscards] = useState(0);
  const closeSettled = useEffectEvent(onClose);
  useEffect(() => {
    if (closeWhenSettled && !dirty) closeSettled();
  }, [closeWhenSettled, dirty]);
  const [discardOpen, setDiscardOpen] = useState(false);
  // Keep out reviews and applies in one click: it is one item, explicit, and
  // reversible from the Excluded section, so no second dialog is asked.
  const previewExclude = usePreviewExclude(album.id, moment.id);
  const excludeEntries = useExcludeEntries(album.id, moment.id);
  const excluding = previewExclude.isPending || excludeEntries.isPending;
  const keepOut = async () => {
    if (!entry || excluding) return;
    const preview = await previewExclude.mutateAsync({
      entry_ids: [entry.id],
      review_token: "",
    });
    await excludeEntries.mutateAsync({
      entry_ids: [entry.id],
      review_token: preview.review_token,
    });
    // The item is gone from this Moment, so any unsaved draft is moot.
    setDraft({});
    setDiscards((count) => count + 1);
    setCloseWhenSettled(true);
  };
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
            {video
              ? "Video details"
              : entry
                ? "Photo details"
                : "Rules & exceptions"}
          </DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            {video
              ? `${entry.filename}. The title shows in every album with this video. Access decisions here override its Moment and the Album.`
              : entry
                ? `Decisions for ${entry.filename} override its Moment and the Album.`
                : `Decisions for ${moment.label} override Album access. Item exceptions still win.`}
          </DialogDescription>
          {entry &&
            (video && entry.playback_url ? (
              // A Curator can watch the video here before choosing its title.
              <video
                aria-label={entry.filename}
                className="mt-4 max-h-64 w-full rounded-sm bg-black"
                controls
                playsInline
                preload="metadata"
                src={entry.playback_url}
              />
            ) : (
              <AlbumImage
                alt={entry.filename}
                className="mt-4 h-auto max-h-40 w-auto max-w-full"
                fallback="No preview available"
                src={entry.available ? entry.thumbnail_url : ""}
              />
            ))}
          {video && (
            <VideoDetails
              albumID={album.id}
              entry={entry}
              key={discards}
              onDirty={setVideoDirty}
              onEdit={() => setCloseWhenSettled(false)}
              onSaved={() => setCloseWhenSettled(true)}
            />
          )}
          {video && (
            <h3 className="mt-6 border-t border-border pt-5 text-xs font-medium">
              Access
            </h3>
          )}
          <Form
            aria-busy={save.isPending}
            aria-label={entry ? "Item access" : "Rules & exceptions"}
            className={video ? "mt-2" : "mt-6"}
            error={save.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (save.isPending) return;
              if (changed.length === 0) {
                changeOpen(false);
                return;
              }
              save.mutate(
                {
                  decisions: changed.map((person) => ({
                    person_id: person.person_id,
                    decision: value(person),
                  })),
                },
                // The saved choices are no longer a draft, whatever the
                // refreshed Album says about them.
                {
                  onSuccess: () => {
                    setDraft({});
                    setCloseWhenSettled(true);
                  },
                },
              );
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
                    setCloseWhenSettled(false);
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
          {entry && (
            <Form
              aria-busy={excluding}
              aria-label="Keep out of this album"
              className="mt-6 border-t border-border pt-5"
              error={previewExclude.error ?? excludeEntries.error}
              onSubmit={(event) => {
                event.preventDefault();
                void keepOut().catch(() => {});
              }}
            >
              <h3 className="text-xs font-medium">This album</h3>
              <p className="mt-1 text-xs text-muted">
                Keep out leaves the {video ? "video" : "photo"} in Immich but
                takes it out of this album. It waits in the Excluded section
                with its access decisions until you add it back.
              </p>
              <Button
                className="mt-3"
                disabled={excluding || save.isPending}
                type="submit"
                variant="outline"
              >
                {excluding ? "Keeping out…" : "Keep out of this album"}
              </Button>
            </Form>
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description={
          video
            ? "The saved title and access stay as they are."
            : "Saved access stays as it is."
        }
        onConfirm={() => {
          setDraft({});
          setDiscards((count) => count + 1);
          setCloseWhenSettled(true);
        }}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title={
          video ? "Discard these changes?" : "Discard these access changes?"
        }
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
