import { useLayoutEffect, useRef, type ComponentProps } from "react";

import { Form } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";

// A controlled yes-or-no dialog. Use it wherever the app would otherwise reach
// for window.confirm: it matches the app's look, returns focus to whatever was
// focused when it opened, and can keep showing a pending state or error while
// the confirmed action runs.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  confirmName = confirmLabel,
  pendingLabel = "Working…",
  cancelLabel = "Cancel",
  onConfirm,
  pending = false,
  error = null,
  onCloseAutoFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  confirmName?: string;
  pendingLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  pending?: boolean;
  error?: unknown;
  onCloseAutoFocus?: ComponentProps<typeof DialogContent>["onCloseAutoFocus"];
}) {
  // There is no Radix trigger to return focus to, so remember the element that
  // was focused before the dialog took it. Layout effects run before Radix
  // moves focus into the dialog.
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (open && document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, [open]);
  return (
    <Dialog
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next);
      }}
      open={open}
    >
      <DialogContent
        onCloseAutoFocus={
          onCloseAutoFocus ??
          ((event) => {
            event.preventDefault();
            const target = returnFocusRef.current;
            if (target?.isConnected) target.focus();
          })
        }
      >
        <DialogTitle className="pr-8 wrap-anywhere">{title}</DialogTitle>
        <DialogDescription className="my-5 text-sm text-muted">
          {description}
        </DialogDescription>
        <Form
          aria-busy={pending}
          aria-label={confirmName}
          error={error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!pending) onConfirm();
          }}
        >
          <Button aria-label={confirmName} disabled={pending} type="submit">
            {pending ? pendingLabel : confirmLabel}
          </Button>
          <Button
            className="ml-3"
            disabled={pending}
            onClick={() => onOpenChange(false)}
            variant="ghost"
          >
            {cancelLabel}
          </Button>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
