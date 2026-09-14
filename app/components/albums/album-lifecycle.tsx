import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import {
  useDeleteAlbum,
  useUnpublishAlbum,
} from "../../hooks/queries/publication";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { AlbumDetail } from "../../types/generated/publishing";
import { ConfirmAction } from "../forms/confirm-action";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Field, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";

// Rare, consequential actions sit last in Album details: hiding a published
// Album again, and permanently deleting Memento's copy of it.
export function DangerZone({ album }: { album: AlbumDetail }) {
  const unpublish = useUnpublishAlbum(album.id);
  const [deleteOpen, setDeleteOpen] = useState(false);
  return (
    <section
      aria-labelledby="danger-zone"
      className="mt-9 border-t border-border pt-6"
    >
      <h3 className="font-heading text-xl" id="danger-zone">
        Danger zone
      </h3>
      {album.published && (
        <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border py-3">
          <p className="text-sm">
            <strong className="block font-medium">Unpublish Album</strong>
            <span className="text-xs text-muted">
              Hides the Album from viewers. Moments, access decisions and
              notification history stay in place.
            </span>
          </p>
          <ConfirmAction
            compact
            description="Viewers lose access immediately. Moments, access decisions and notification history stay in place. You can publish again later."
            error={unpublish.error}
            label="Unpublish Album"
            onConfirm={() => unpublish.mutateAsync()}
            pending={unpublish.isPending}
          />
        </div>
      )}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border py-3">
        <p className="text-sm">
          <strong className="block font-medium">Delete Album</strong>
          <span className="text-xs text-muted">
            Removes Memento's curation and access decisions. Immich is never
            changed.
          </span>
        </p>
        <Button
          className="text-destructive"
          onClick={() => setDeleteOpen(true)}
          size="sm"
          type="button"
          variant="outline"
        >
          Delete Album
        </Button>
      </div>
      {deleteOpen && (
        <DeleteAlbumDialog album={album} onClose={() => setDeleteOpen(false)} />
      )}
    </section>
  );
}

function DeleteAlbumDialog({
  album,
  onClose,
}: {
  album: AlbumDetail;
  onClose: () => void;
}) {
  const remove = useDeleteAlbum(album.id);
  const navigate = useNavigate();
  useEffect(() => {
    if (remove.isSuccess) void navigate("/curator/albums", { replace: true });
  }, [remove.isSuccess, navigate]);
  const [title, setTitle] = useState("");
  const [discardOpen, setDiscardOpen] = useState(false);
  useUnsavedChanges(
    (title.length > 0 || remove.isPending) && !remove.isSuccess,
    true,
  );
  const returnFocus = useReturnFocus();
  function close() {
    if (remove.isPending) return;
    if (title) setDiscardOpen(true);
    else onClose();
  }
  return (
    <>
      <Dialog onOpenChange={(open) => !open && close()} open>
        <DialogContent onCloseAutoFocus={returnFocus}>
          <DialogTitle>Permanently delete this Album?</DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            This deletes this Album's Memento curation and access decisions.
            Immich stays untouched. Media shared with other Albums keep their
            metadata and remain available there. This cannot be undone.
          </DialogDescription>
          <p className="mt-5 text-sm">
            Type <strong>{album.title}</strong> to confirm.
          </p>
          <Form
            aria-busy={remove.isPending}
            aria-label="Delete Album"
            className="mt-4"
            error={remove.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (title !== album.title || remove.isPending) return;
              remove.mutate({ title });
            }}
          >
            <fieldset disabled={remove.isPending}>
              <Field
                autoComplete="off"
                error={fieldErrors(remove.error).title}
                label="Type the Album title"
                name="title"
                onChange={(event) => {
                  remove.reset();
                  setTitle(event.target.value);
                }}
                value={title}
              />
              <div className="flex flex-wrap gap-2">
                <Button disabled={title !== album.title} type="submit">
                  {remove.isPending ? "Deleting…" : "Permanently delete Album"}
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
        confirmLabel="Keep Album"
        description="The Album and its access decisions stay unchanged."
        onConfirm={onClose}
        onOpenChange={setDiscardOpen}
        open={discardOpen}
        title="Cancel Album deletion?"
      />
    </>
  );
}
