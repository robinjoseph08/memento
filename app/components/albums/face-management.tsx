import { ExternalLink } from "lucide-react";
import { useCallback, useEffect, useId, useState } from "react";

import {
  useCreatePersonFromFace,
  useIgnoreFace,
  useLinkFace,
  usePeople,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { initials } from "../../lib/initials";
import type { Person } from "../../types/generated/identity";
import type { FaceRecord } from "../../types/generated/publishing";
import { Field, FieldError, Form } from "../people/form-fields";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";

const createValue = "create";
// Enough rows to show the named faces of a typical event before asking for more.
const initialFaceCount = 5;

function faceName(face: FaceRecord) {
  return face.source_name || "Unnamed face";
}

function occurrences(face: FaceRecord) {
  return `In ${face.occurrences} ${face.occurrences === 1 ? "item" : "items"}`;
}

// Named faces first, since they are the quick wins, then by how much of the
// Moment each face appears in.
function byUsefulness(left: FaceRecord, right: FaceRecord) {
  if (!!left.source_name !== !!right.source_name)
    return left.source_name ? -1 : 1;
  if (left.occurrences !== right.occurrences)
    return right.occurrences - left.occurrences;
  return left.source_name.localeCompare(right.source_name);
}

// The one person whose display name matches the Immich name exactly, if any.
function matchingPerson(face: FaceRecord, people: Person[]) {
  const name = face.source_name.trim().toLowerCase();
  if (!name) return undefined;
  const matches = people.filter(
    (person) => person.display_name.trim().toLowerCase() === name,
  );
  return matches.length === 1 ? matches[0] : undefined;
}

// Faces Immich recognized that Memento cannot yet attribute to a person.
// Linked faces already appear as people above, so only the unresolved ones
// need attention here.
export function UnlinkedFaces({ faces }: { faces: FaceRecord[] }) {
  const unlinked = faces
    .filter((face) => !face.person_id && !face.ignored)
    .sort(byUsefulness);
  const ignored = faces
    .filter((face) => !face.person_id && face.ignored)
    .sort(byUsefulness);
  const [openID, setOpenID] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [dirtyFaceIDs, setDirtyFaceIDs] = useState(() => new Set<string>());
  const setFaceDirty = useCallback((sourceID: string, dirty: boolean) => {
    setDirtyFaceIDs((current) => {
      if (current.has(sourceID) === dirty) return current;
      const next = new Set(current);
      if (dirty) next.add(sourceID);
      else next.delete(sourceID);
      return next;
    });
  }, []);
  useUnsavedChanges(dirtyFaceIDs.size > 0, true);
  if (unlinked.length === 0 && ignored.length === 0) return null;
  const visible = showAll ? unlinked : unlinked.slice(0, initialFaceCount);
  const row = (face: FaceRecord, canIgnore: boolean) => (
    <FaceRow
      canIgnore={canIgnore}
      face={face}
      key={face.source_id}
      onDirtyChange={setFaceDirty}
      onOpenChange={(open) => setOpenID(open ? face.source_id : null)}
      open={openID === face.source_id}
    />
  );
  return (
    <section aria-label="Unlinked faces" className="mt-2">
      <p className="text-xs leading-relaxed text-muted">
        Immich recognized these people. Link each one to a person to get access
        suggestions. Duplicates are best merged in Immich, then checked again
        here.
      </p>
      {unlinked.length > 0 ? (
        <ul className="mt-2">{visible.map((face) => row(face, true))}</ul>
      ) : (
        <p className="mt-3 text-xs text-muted">Every face is linked.</p>
      )}
      {unlinked.length > initialFaceCount && (
        <Button
          className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
          onClick={() => setShowAll(!showAll)}
          type="button"
          variant="ghost"
        >
          {showAll ? "Show fewer faces" : `Show all ${unlinked.length} faces`}
        </Button>
      )}
      {ignored.length > 0 && (
        <details className="mt-3">
          <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
            Ignored faces ({ignored.length})
          </summary>
          <ul>{ignored.map((face) => row(face, false))}</ul>
        </details>
      )}
    </section>
  );
}

function FaceRow({
  face,
  open,
  canIgnore,
  onOpenChange,
  onDirtyChange,
}: {
  face: FaceRecord;
  open: boolean;
  canIgnore: boolean;
  onOpenChange: (open: boolean) => void;
  onDirtyChange: (sourceID: string, dirty: boolean) => void;
}) {
  const name = faceName(face);
  return (
    <li className="border-t border-border py-3">
      <div className="flex items-center gap-3">
        <Avatar className="size-10">
          <AvatarImage alt="" src={face.thumbnail_url} />
          <AvatarFallback className="text-xs">{initials(name)}</AvatarFallback>
        </Avatar>
        <p className="min-w-0 flex-1 text-sm">
          <strong className="block truncate font-medium">{name}</strong>
          <span className="block text-xs text-muted">{occurrences(face)}</span>
        </p>
        {face.immich_url && (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <a
                  aria-label={`Open ${name} in Immich`}
                  className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted hover:bg-surface hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring pointer-coarse:size-11"
                  href={face.immich_url}
                  rel="noreferrer"
                  target="_blank"
                >
                  <ExternalLink
                    aria-hidden="true"
                    className="size-4"
                    strokeWidth={1.5}
                  />
                </a>
              </TooltipTrigger>
              <TooltipContent side="bottom">
                Open in Immich to merge or rename
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )}
        {!open && (
          <Button
            aria-label={`Link ${name}`}
            onClick={() => onOpenChange(true)}
            size="sm"
            type="button"
            variant="outline"
          >
            Link
          </Button>
        )}
      </div>
      {open && (
        <LinkFaceForm
          canIgnore={canIgnore}
          face={face}
          onClose={() => onOpenChange(false)}
          onDirtyChange={onDirtyChange}
        />
      )}
    </li>
  );
}

function LinkFaceForm({
  face,
  canIgnore,
  onClose,
  onDirtyChange,
}: {
  face: FaceRecord;
  canIgnore: boolean;
  onClose: () => void;
  onDirtyChange: (sourceID: string, dirty: boolean) => void;
}) {
  const name = faceName(face);
  const sourceName = face.source_name.trim();
  const { data: people, isPending: loadingPeople } = usePeople("");
  const availablePeople = (people ?? []).filter(
    (person) => !person.deactivated_at,
  );
  // An exact name match is the obvious choice, so it starts selected once the
  // people list has loaded. Otherwise creating a person is the usual outcome.
  const [choice, setChoice] = useState<string | null>(null);
  const suggestedID = matchingPerson(face, availablePeople)?.id ?? "";
  const personID = choice ?? (people ? suggestedID || createValue : "");
  const [displayName, setDisplayName] = useState("");
  const [choiceError, setChoiceError] = useState("");
  const choiceId = useId();
  const sourceErrorId = useId();
  const creating = personID === createValue;
  const link = useLinkFace();
  const create = useCreatePersonFromFace();
  const ignore = useIgnoreFace();
  const pending = link.isPending || create.isPending || ignore.isPending;
  const dirty = choice !== null || !!displayName || pending;
  useEffect(() => {
    onDirtyChange(face.source_id, dirty);
  }, [dirty, face.source_id, onDirtyChange]);
  useEffect(
    () => () => onDirtyChange(face.source_id, false),
    [face.source_id, onDirtyChange],
  );
  const createErrors = fieldErrors(create.error);
  const linkErrors = fieldErrors(link.error);
  const sourceError = creating
    ? createErrors.source_face_id
    : linkErrors.source_face_id;
  const options = availablePeople.map((person) => ({
    value: person.id,
    label: person.display_name,
    leading: (
      <Avatar className="size-6">
        {person.avatar_url && <AvatarImage alt="" src={person.avatar_url} />}
        <AvatarFallback className="text-[10px]">
          {initials(person.display_name)}
        </AvatarFallback>
      </Avatar>
    ),
  }));

  return (
    <Form
      aria-busy={pending}
      aria-label={`Link ${name}`}
      className="mt-3"
      error={creating ? create.error : (link.error ?? ignore.error)}
      onSubmit={(event) => {
        event.preventDefault();
        if (!personID) {
          setChoiceError("Choose a person.");
          return;
        }
        if (creating) {
          create.mutate(
            {
              display_name: sourceName || displayName,
              source_face_id: face.source_id,
            },
            { onSuccess: onClose },
          );
          return;
        }
        link.mutate(
          { personID, body: { source_face_id: face.source_id } },
          { onSuccess: onClose },
        );
      }}
    >
      <fieldset
        aria-describedby={sourceError ? sourceErrorId : undefined}
        aria-invalid={!!sourceError}
        disabled={pending}
        tabIndex={sourceError ? -1 : undefined}
      >
        <FieldError error={sourceError} id={sourceErrorId} />
        <div className="mb-4">
          <label className="block text-xs font-medium" id={`${choiceId}-label`}>
            Person
          </label>
          <Combobox
            action={{
              value: createValue,
              label: sourceName
                ? `Create "${sourceName}"`
                : "Create a new person",
            }}
            aria-describedby={choiceError ? `${choiceId}-error` : undefined}
            aria-invalid={!!choiceError}
            aria-labelledby={`${choiceId}-label`}
            className="mt-2"
            emptyText="No one by that name yet."
            onChange={(value) => {
              setChoice(value);
              setChoiceError("");
              link.reset();
              create.reset();
            }}
            options={options}
            placeholder={loadingPeople ? "Loading people…" : "Choose a person"}
            searchPlaceholder="Search people"
            value={personID}
          />
          <FieldError error={choiceError} id={`${choiceId}-error`} />
        </div>
        {creating && !sourceName && (
          <Field
            error={createErrors.display_name}
            label="Display name"
            maxLength={100}
            onChange={(event) => {
              create.reset();
              setDisplayName(event.target.value);
            }}
            required
            value={displayName}
          />
        )}
        <div className="flex flex-wrap gap-2">
          <Button size="sm" type="submit">
            {creating
              ? create.isPending
                ? "Creating…"
                : "Create and link"
              : link.isPending
                ? "Linking…"
                : "Link face"}
          </Button>
          {canIgnore && (
            <Button
              onClick={() => ignore.mutate(face.source_id)}
              size="sm"
              type="button"
              variant="ghost"
            >
              {ignore.isPending ? "Ignoring…" : "Ignore"}
            </Button>
          )}
          <Button onClick={onClose} size="sm" type="button" variant="ghost">
            Cancel
          </Button>
        </div>
      </fieldset>
    </Form>
  );
}
