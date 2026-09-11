import { useId, useState } from "react";

import {
  useMergeMoments,
  useMoveEntries,
  usePreviewMerge,
  usePreviewMove,
  usePreviewSplit,
  useSplitMoment,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  AlbumDetail,
  Decision,
  MergeMomentsRequest,
  Moment,
  MoveEntriesRequest,
  SplitMomentRequest,
  StructurePreview,
} from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Field, FieldError, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import { Combobox, type ComboboxOption } from "../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { AlbumImage } from "./album-image";

export type StructureOperation = "move" | "split" | "merge";

function SelectField({
  label,
  value,
  onChange,
  options,
  placeholder,
  error,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: ComboboxOption[];
  placeholder: string;
  error?: string;
}) {
  const id = useId();
  return (
    <div className="mb-5">
      <label className="block text-xs font-medium" id={`${id}-label`}>
        {label}
      </label>
      <Combobox
        aria-describedby={error ? `${id}-error` : undefined}
        aria-invalid={!!error}
        aria-labelledby={`${id}-label`}
        className="mt-2"
        onChange={onChange}
        options={options}
        placeholder={placeholder}
        value={value}
      />
      <FieldError error={error} id={`${id}-error`} />
    </div>
  );
}

function VisibilityReview({ preview }: { preview: StructurePreview }) {
  return (
    <section
      aria-label="Visibility review"
      className="my-6 border-y border-border py-5"
    >
      <h3 className="font-heading text-xl">Visibility after saving</h3>
      {preview.removes_moment && (
        <p className="mt-3 text-sm">
          The empty Moment will be removed after its final item moves.
        </p>
      )}
      {preview.changes.length === 0 ? (
        <p className="mt-3 text-sm text-muted">No one gains or loses media.</p>
      ) : (
        <div className="mt-3 space-y-2">
          {preview.changes.map((change) => (
            <p className="py-1 text-sm" key={change.person_id}>
              <strong>{change.display_name}</strong>{" "}
              <span className="text-muted">
                {change.gained_entry_ids.length > 0 &&
                  `gains ${change.gained_entry_ids.length}`}
                {change.gained_entry_ids.length > 0 &&
                  change.lost_entry_ids.length > 0 &&
                  ", "}
                {change.lost_entry_ids.length > 0 &&
                  `loses ${change.lost_entry_ids.length}`}
              </span>
            </p>
          ))}
        </div>
      )}
    </section>
  );
}

export function StructureEditor({
  album,
  moment,
  selectedEntryIDs,
  operation,
  onClose,
  onSaved,
}: {
  album: AlbumDetail;
  moment: Moment;
  selectedEntryIDs: string[];
  operation: StructureOperation;
  onClose: () => void;
  onSaved: () => void;
}) {
  const others = album.moments.filter((item) => item.id !== moment.id);
  const selected = moment.entries.filter((entry) =>
    selectedEntryIDs.includes(entry.id),
  );
  const [targetID, setTargetID] = useState(others[0]?.id ?? "");
  const [title, setTitle] = useState("");
  // A merge keeps one of the two existing covers, so the destination's cover
  // is the natural default. Moves and splits pick covers on the server.
  const defaultCoverID = others[0]?.cover_entry_id ?? "";
  const [newCoverID, setNewCoverID] = useState(defaultCoverID);
  const [resolutions, setResolutions] = useState<Record<string, Decision>>({});
  const [preview, setPreview] = useState<StructurePreview | null>(null);
  const previewMove = usePreviewMove(album.id, moment.id);
  const move = useMoveEntries(album.id, moment.id);
  const previewSplit = usePreviewSplit(album.id, moment.id);
  const split = useSplitMoment(album.id, moment.id);
  const previewMerge = usePreviewMerge(album.id, moment.id);
  const merge = useMergeMoments(album.id, moment.id);
  const target = others.find((item) => item.id === targetID);
  const coverOf = (item: Moment) =>
    item.entries.find((entry) => entry.id === item.cover_entry_id);
  const mergeCovers = [moment, ...(target ? [target] : [])].flatMap((item) => {
    const cover = coverOf(item);
    return cover ? [{ moment: item, entry: cover }] : [];
  });
  const pending =
    previewMove.isPending ||
    move.isPending ||
    previewSplit.isPending ||
    split.isPending ||
    previewMerge.isPending ||
    merge.isPending;
  const error =
    previewMove.error ??
    move.error ??
    previewSplit.error ??
    split.error ??
    previewMerge.error ??
    merge.error;
  const errors = fieldErrors(error);
  const dirty =
    pending ||
    preview !== null ||
    title !== "" ||
    targetID !== (others[0]?.id ?? "") ||
    newCoverID !== defaultCoverID ||
    Object.keys(resolutions).length > 0;
  useUnsavedChanges(dirty, true);
  const [discardOpen, setDiscardOpen] = useState(false);
  function changeOpen(next: boolean) {
    if (next || pending) return;
    if (dirty) setDiscardOpen(true);
    else onClose();
  }

  let request: MoveEntriesRequest | SplitMomentRequest | MergeMomentsRequest;
  if (operation === "move") {
    request = {
      entry_ids: selectedEntryIDs,
      destination_moment_id: targetID,
      review_token: preview?.review_token ?? "",
    };
  } else if (operation === "split") {
    request = {
      entry_ids: selectedEntryIDs,
      new_title: title,
      review_token: preview?.review_token ?? "",
    };
  } else {
    request = {
      target_moment_id: targetID,
      title,
      cover_entry_id: newCoverID,
      resolutions: Object.entries(resolutions).map(([person_id, decision]) => ({
        person_id,
        decision,
      })),
      review_token: preview?.review_token ?? "",
    };
  }

  function clearReview() {
    setPreview(null);
    previewMove.reset();
    move.reset();
    previewSplit.reset();
    split.reset();
    previewMerge.reset();
    merge.reset();
  }

  function review() {
    if (operation === "move") {
      previewMove.mutate(
        { ...(request as MoveEntriesRequest), review_token: "" },
        { onSuccess: setPreview },
      );
    } else if (operation === "split") {
      previewSplit.mutate(
        { ...(request as SplitMomentRequest), review_token: "" },
        { onSuccess: setPreview },
      );
    } else {
      previewMerge.mutate(
        { ...(request as MergeMomentsRequest), review_token: "" },
        { onSuccess: setPreview },
      );
    }
  }

  function commit() {
    const onError = () => setPreview(null);
    if (operation === "move") {
      move.mutate(request as MoveEntriesRequest, {
        onSuccess: onSaved,
        onError,
      });
    } else if (operation === "split") {
      split.mutate(request as SplitMomentRequest, {
        onSuccess: onSaved,
        onError,
      });
    } else {
      merge.mutate(request as MergeMomentsRequest, {
        onSuccess: onSaved,
        onError,
      });
    }
  }

  const titleText =
    operation === "move"
      ? "Move media"
      : operation === "split"
        ? "Split Moment"
        : "Merge Moments";
  const coverLeaves =
    operation !== "merge" &&
    selectedEntryIDs.includes(moment.cover_entry_id) &&
    selectedEntryIDs.length < moment.entries.length;

  return (
    <>
      <Dialog onOpenChange={changeOpen} open>
        <DialogContent className="max-w-xl">
          <DialogTitle>{titleText}</DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            {operation === "merge"
              ? `Combine every item in ${moment.label} with another Moment.`
              : `${selected.length} ${selected.length === 1 ? "item" : "items"} selected from ${moment.label}.`}
          </DialogDescription>
          <Form
            aria-busy={pending}
            aria-label={titleText}
            className="mt-6"
            error={error}
            onSubmit={(event) => {
              event.preventDefault();
              if (preview?.ready && preview.review_token) commit();
              else review();
            }}
          >
            <fieldset disabled={pending}>
              {operation !== "split" && (
                <SelectField
                  error={
                    errors.destination_moment_id ?? errors.target_moment_id
                  }
                  label="Destination Moment"
                  onChange={(value) => {
                    setTargetID(value);
                    if (operation === "merge")
                      setNewCoverID(
                        others.find((item) => item.id === value)
                          ?.cover_entry_id ?? "",
                      );
                    clearReview();
                  }}
                  options={others.map((item) => ({
                    value: item.id,
                    label: item.label,
                  }))}
                  placeholder="Choose a Moment"
                  value={targetID}
                />
              )}
              {operation !== "move" && (
                <Field
                  error={errors.new_title ?? errors.title}
                  label={
                    operation === "split"
                      ? "New Moment title"
                      : "Merged Moment title"
                  }
                  maxLength={200}
                  onChange={(event) => {
                    setTitle(event.target.value);
                    clearReview();
                  }}
                  placeholder={
                    operation === "merge"
                      ? target?.label
                      : "Generated date label"
                  }
                  value={title}
                />
              )}
              {operation === "merge" && (
                <fieldset className="mb-5">
                  <legend className="block text-xs font-medium">
                    Merged Moment cover
                  </legend>
                  <div className="mt-2 flex flex-wrap gap-3">
                    {mergeCovers.map(({ moment: item, entry }) => (
                      <label
                        className="flex max-w-40 cursor-pointer flex-col gap-2 text-xs"
                        key={item.id}
                      >
                        <input
                          aria-label={`Keep the cover of ${item.label}`}
                          checked={newCoverID === entry.id}
                          className="peer sr-only"
                          name="merged_cover"
                          onChange={() => {
                            setNewCoverID(entry.id);
                            clearReview();
                          }}
                          type="radio"
                          value={entry.id}
                        />
                        <span className="block rounded-sm peer-checked:outline-2 peer-checked:outline-offset-2 peer-checked:outline-primary peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-ring">
                          <AlbumImage
                            alt={entry.filename}
                            className="h-auto max-h-28 w-auto max-w-40"
                            fallback="No preview available"
                            src={entry.available ? entry.thumbnail_url : ""}
                          />
                        </span>
                        <span className="text-muted">
                          <span className="block">{item.label}</span>
                          <span className="block">
                            {item.id === targetID ? "Destination" : "Merging"}
                          </span>
                        </span>
                      </label>
                    ))}
                  </div>
                  <FieldError
                    error={errors.cover_entry_id}
                    id="merged-cover-error"
                  />
                </fieldset>
              )}
              <FieldError error={errors.entry_ids} id="entry-ids-error" />
              {operation === "split" && (
                <p className="mb-5 text-xs leading-relaxed text-muted">
                  Both Moments keep the existing access decisions, and the new
                  Moment starts with its earliest item as its cover. Splitting
                  alone changes no one's media access.
                </p>
              )}
              {coverLeaves && (
                <p className="mb-5 text-xs leading-relaxed text-muted">
                  This Moment's cover is leaving, so its earliest remaining item
                  becomes the cover.
                </p>
              )}
              {preview && preview.conflicts.length > 0 && (
                <fieldset className="mb-6 border-t border-border pt-5">
                  <legend className="font-heading text-xl">
                    Choose the combined audience
                  </legend>
                  <p className="mt-2 mb-4 text-xs text-muted">
                    These Moments differ. Choose access for every listed Person.
                  </p>
                  <FieldError
                    error={errors.resolutions}
                    id="resolutions-error"
                  />
                  {preview.conflicts.map((conflict) => (
                    <SelectField
                      key={conflict.person_id}
                      label={conflict.display_name}
                      onChange={(decision) => {
                        setResolutions((current) => ({
                          ...current,
                          [conflict.person_id]: decision,
                        }));
                        setPreview({
                          ...preview,
                          ready: false,
                          review_token: "",
                        });
                      }}
                      options={[
                        { value: "inherit", label: "No Moment decision" },
                        { value: "allow", label: "Allow" },
                        { value: "deny", label: "Exclude" },
                      ]}
                      placeholder={`${conflict.source} here, ${conflict.target} there`}
                      value={resolutions[conflict.person_id] ?? ""}
                    />
                  ))}
                </fieldset>
              )}
              {preview?.ready && <VisibilityReview preview={preview} />}
              <div className="flex flex-wrap gap-2">
                <Button type="submit">
                  {pending
                    ? "Saving…"
                    : preview?.ready
                      ? `Confirm ${operation}`
                      : `Review ${operation}`}
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
        description="The album stays as it is and your choices here will not be saved."
        onConfirm={onClose}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Discard this structural change?"
      />
    </>
  );
}
