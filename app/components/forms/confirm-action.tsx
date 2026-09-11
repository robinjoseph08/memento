import { useState } from "react";

import { Button } from "../ui/button";
import { ConfirmDialog } from "./confirm-dialog";

// A button that asks for confirmation before running an action, and keeps the
// prompt open with its error when the action fails.
export function ConfirmAction({
  label,
  triggerLabel = label,
  description,
  pending,
  error,
  onConfirm,
  disabled = false,
  compact = false,
}: {
  label: string;
  triggerLabel?: string;
  description: string;
  pending: boolean;
  error: unknown;
  onConfirm: () => Promise<unknown>;
  disabled?: boolean;
  compact?: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={label}
        disabled={disabled || pending}
        onClick={() => setOpen(true)}
        size={compact ? "sm" : "default"}
        variant="outline"
      >
        {triggerLabel}
      </Button>
      <ConfirmDialog
        confirmLabel={triggerLabel}
        confirmName={label}
        description={description}
        error={error}
        onConfirm={() => {
          void onConfirm()
            .then(() => setOpen(false))
            .catch(() => {});
        }}
        onOpenChange={setOpen}
        open={open}
        pending={pending}
        title={`${label}?`}
      />
    </>
  );
}
