import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import {
  useDeleteAlbum,
  useUnpublishAlbum,
} from "../../hooks/queries/publication";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { AlbumDetail } from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Field, Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";

export function AlbumLifecycle({
  album,
  disabled,
}: {
  album: AlbumDetail;
  disabled: boolean;
}) {
  const unpublish = useUnpublishAlbum(album.id);
  const [unpublishOpen, setUnpublishOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  return (
    <div className="mt-5">
      <Button
        className="text-destructive"
        disabled={disabled}
        onClick={() => setDeleteOpen(true)}
        variant="ghost"
      >
        Delete Album
      </Button>
      {deleteOpen && (
        <DeleteAlbumDialog album={album} onClose={() => setDeleteOpen(false)} />
      )}
      {album.published && (
        <Button
          disabled={disabled}
          onClick={() => setUnpublishOpen(true)}
          variant="outline"
        >
          Unpublish Album
        </Button>
      )}
      <ConfirmDialog
        confirmLabel="Unpublish"
        description="Viewers lose access immediately. Moments, access decisions and notification history stay in place. You can publish again later."
        error={unpublish.error}
        onConfirm={() =>
          unpublish.mutate(undefined, {
            onSuccess: () => setUnpublishOpen(false),
          })
        }
        onOpenChange={setUnpublishOpen}
        open={unpublishOpen}
        pending={unpublish.isPending}
        title="Unpublish this Album?"
      />
    </div>
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
    if (remove.isSuccess) void navigate("/curator", { replace: true });
  }, [remove.isSuccess, navigate]);
  const [title, setTitle] = useState("");
  const [discardOpen, setDiscardOpen] = useState(false);
  useUnsavedChanges(
    (title.length > 0 || remove.isPending) && !remove.isSuccess,
    true,
  );
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, []);
  function close() {
    if (remove.isPending) return;
    if (title) setDiscardOpen(true);
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
