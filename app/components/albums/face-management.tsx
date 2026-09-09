import { useCallback, useEffect, useId, useState } from "react";

import {
  useCreatePersonFromFace,
  useIgnoreFace,
  useLinkFace,
  usePeople,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { FaceRecord } from "../../types/generated/publishing";
import { Field, FieldError, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { AlbumImage } from "./album-image";

export function FaceManagement({ faces }: { faces: FaceRecord[] }) {
  const visible = faces.filter((face) => !face.ignored);
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
  if (visible.length === 0) return null;
  return (
    <section
      aria-labelledby="immich-faces"
      className="mt-8 border-t border-border pt-6"
    >
      <h3 className="text-sm font-medium" id="immich-faces">
        Faces from Immich
      </h3>
      <p className="mt-2 text-xs leading-relaxed text-muted">
        Link recognized faces to Memento People before using them as access
        suggestions.
      </p>
      <div className="mt-4 space-y-4">
        {visible.map((face) =>
          face.person_id ? (
            <div className="flex items-center gap-3" key={face.source_id}>
              <AlbumImage
                alt=""
                className="size-11 shrink-0 rounded-full object-cover"
                fallback="Face unavailable"
                src={face.thumbnail_url}
              />
              <p className="min-w-0 text-xs">
                <strong className="block truncate font-medium">
                  {face.person_name}
                </strong>
                <span className="text-muted">
                  Linked, detected in {face.occurrences}{" "}
                  {face.occurrences === 1 ? "item" : "items"}
                </span>
              </p>
            </div>
          ) : (
            <UnlinkedFace
              face={face}
              key={face.source_id}
              onDirtyChange={setFaceDirty}
            />
          ),
        )}
      </div>
    </section>
  );
}

function UnlinkedFace({
  face,
  onDirtyChange,
}: {
  face: FaceRecord;
  onDirtyChange: (sourceID: string, dirty: boolean) => void;
}) {
  const { data: people = [], isPending: loadingPeople } = usePeople("");
  const availablePeople = people.filter((person) => !person.deactivated_at);
  const [personID, setPersonID] = useState("");
  const [createMode, setCreateMode] = useState(false);
  const [displayName, setDisplayName] = useState("");
  const [personError, setPersonError] = useState("");
  const linkErrorId = useId();
  const sourceErrorId = useId();
  const link = useLinkFace(personID || "missing");
  const create = useCreatePersonFromFace();
  const ignore = useIgnoreFace();
  const pending = link.isPending || create.isPending || ignore.isPending;
  const dirty = !!personID || !!displayName || pending;
  useEffect(() => {
    onDirtyChange(face.source_id, dirty);
  }, [dirty, face.source_id, onDirtyChange]);
  useEffect(
    () => () => onDirtyChange(face.source_id, false),
    [face.source_id, onDirtyChange],
  );
  const error = link.error ?? create.error ?? ignore.error;
  const createErrors = fieldErrors(create.error);
  const linkErrors = fieldErrors(link.error);

  return (
    <div className="border-t border-border pt-4">
      <div className="mb-4 flex items-center gap-3">
        <AlbumImage
          alt={face.source_name || "Unlinked face"}
          className="size-14 shrink-0 rounded-full object-cover"
          fallback="Face unavailable"
          src={face.thumbnail_url}
        />
        <p className="min-w-0 text-xs">
          <strong className="block truncate font-medium">
            {face.source_name || "Unlinked face"}
          </strong>
          <span className="text-muted">
            Detected in {face.occurrences}{" "}
            {face.occurrences === 1 ? "item" : "items"}
          </span>
        </p>
      </div>
      {createMode ? (
        <Form
          aria-busy={pending}
          aria-label={`Create Person for ${face.source_name || "face"}`}
          error={create.error}
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate({
              display_name: displayName,
              source_face_id: face.source_id,
            });
          }}
        >
          <fieldset
            aria-describedby={
              createErrors.source_face_id
                ? `${sourceErrorId}-create`
                : undefined
            }
            aria-invalid={!!createErrors.source_face_id}
            disabled={pending}
            tabIndex={createErrors.source_face_id ? -1 : undefined}
          >
            <FieldError
              error={createErrors.source_face_id}
              id={`${sourceErrorId}-create`}
            />
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
            <div className="flex flex-wrap gap-2">
              <Button size="sm" type="submit">
                {create.isPending ? "Creating…" : "Create and link"}
              </Button>
              <Button
                onClick={() => {
                  setCreateMode(false);
                  setDisplayName("");
                  create.reset();
                }}
                size="sm"
                type="button"
                variant="outline"
              >
                Cancel
              </Button>
            </div>
          </fieldset>
        </Form>
      ) : (
        <Form
          aria-busy={pending}
          aria-label={`Link ${face.source_name || "face"}`}
          error={error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!personID) {
              setPersonError("Choose a Person.");
              return;
            }
            link.mutate({ source_face_id: face.source_id });
          }}
        >
          <fieldset
            aria-describedby={
              linkErrors.source_face_id ? `${sourceErrorId}-link` : undefined
            }
            aria-invalid={!!linkErrors.source_face_id}
            disabled={pending}
            tabIndex={linkErrors.source_face_id ? -1 : undefined}
          >
            <FieldError
              error={linkErrors.source_face_id}
              id={`${sourceErrorId}-link`}
            />
            <label className="block text-xs font-medium">
              Existing Person
              <Select
                onValueChange={(value) => {
                  setPersonID(value);
                  setPersonError("");
                  link.reset();
                }}
                value={personID}
              >
                <SelectTrigger
                  aria-describedby={personError ? linkErrorId : undefined}
                  aria-invalid={!!personError}
                  className="mt-2"
                >
                  <SelectValue
                    placeholder={
                      loadingPeople ? "Loading People…" : "Choose a Person"
                    }
                  />
                </SelectTrigger>
                <SelectContent>
                  {availablePeople.map((person) => (
                    <SelectItem key={person.id} value={person.id}>
                      {person.display_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </label>
            <FieldError error={personError} id={linkErrorId} />
            <div className="flex flex-wrap gap-2">
              <Button size="sm" type="submit">
                {link.isPending ? "Linking…" : "Link face"}
              </Button>
              <Button
                onClick={() => setCreateMode(true)}
                size="sm"
                type="button"
                variant="outline"
              >
                Create Person
              </Button>
              <Button
                onClick={() => ignore.mutate(face.source_id)}
                size="sm"
                type="button"
                variant="ghost"
              >
                {ignore.isPending ? "Ignoring…" : "Ignore"}
              </Button>
            </div>
          </fieldset>
        </Form>
      )}
    </div>
  );
}
