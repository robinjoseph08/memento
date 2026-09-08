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
  description,
  pending,
  error,
  onConfirm,
  disabled = false,
}: {
  label: string;
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
        <Button disabled={disabled} variant="outline">
          {label}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle className="pr-8">{label}?</DialogTitle>
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
          <Button disabled={pending} type="submit">
            {pending ? "Working…" : label}
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
