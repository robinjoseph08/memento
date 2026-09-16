import type { LucideIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "../ui/button";
import { ConfirmDialog } from "./confirm-dialog";

// A button that asks for confirmation before running an action, and keeps the
// prompt open with its error when the action fails.
export function ConfirmAction({
  label,
  triggerLabel = label,
  confirmLabel = triggerLabel,
  description,
  pending,
  error,
  onConfirm,
  disabled = false,
  compact = false,
  icon: Icon,
}: {
  label: string;
  triggerLabel?: string;
  // confirmLabel names the dialog's confirm button when the trigger's short
  // label would not explain what happens next.
  confirmLabel?: string;
  description: string;
  pending: boolean;
  error: unknown;
  onConfirm: () => Promise<unknown>;
  disabled?: boolean;
  compact?: boolean;
  icon?: LucideIcon;
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
        {Icon && (
          <Icon aria-hidden="true" className="size-4" strokeWidth={1.5} />
        )}
        {triggerLabel}
      </Button>
      <ConfirmDialog
        confirmLabel={confirmLabel}
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
