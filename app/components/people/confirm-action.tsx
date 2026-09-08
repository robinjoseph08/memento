import { useState } from "react";

import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "../ui/dialog";
import { Form } from "./form-fields";

export function ConfirmAction({
  label,
  triggerLabel = label,
  description,
  pending,
  error,
  onConfirm,
  disabled = false,
}: {
  label: string;
  triggerLabel?: string;
  description: string;
  pending: boolean;
  error: unknown;
  onConfirm: () => Promise<unknown>;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog
      onOpenChange={(next) => {
        if (!pending) setOpen(next);
      }}
      open={open}
    >
      <DialogTrigger asChild>
        <Button
          aria-label={label}
          disabled={disabled || pending}
          variant="outline"
        >
          {triggerLabel}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle className="pr-8 wrap-anywhere">{label}?</DialogTitle>
        <DialogDescription className="my-5 text-sm text-muted">
          {description}
        </DialogDescription>
        <Form
          aria-busy={pending}
          aria-label={label}
          error={error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!pending)
              void onConfirm()
                .then(() => setOpen(false))
                .catch(() => {});
          }}
        >
          <Button aria-label={label} disabled={pending} type="submit">
            {pending ? "Working…" : triggerLabel}
          </Button>
          <Button
            className="ml-3"
            disabled={pending}
            onClick={() => setOpen(false)}
            variant="ghost"
          >
            Cancel
          </Button>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
