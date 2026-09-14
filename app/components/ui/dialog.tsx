import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";
import { Button } from "./button";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export function DialogTitle({
  className,
  ...props
}: ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      className={cn(
        "font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]",
        className,
      )}
      {...props}
    />
  );
}
export const DialogDescription = DialogPrimitive.Description;

// A press outside dismisses only for the primary button. A mouse's back and
// forward buttons must reach the browser as history navigation, not close a
// dialog whose open state lives in the URL and then reopen it on the way back.
export function primaryButtonOnly(
  handler: ComponentProps<
    typeof DialogPrimitive.Content
  >["onPointerDownOutside"],
): ComponentProps<typeof DialogPrimitive.Content>["onPointerDownOutside"] {
  return (event) => {
    if (event.detail.originalEvent.button > 0) {
      event.preventDefault();
      return;
    }
    handler?.(event);
  };
}

export function DialogContent({
  className,
  children,
  onPointerDownOutside,
  ...props
}: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/55" />
      <DialogPrimitive.Content
        className={cn(
          "fixed top-1/2 left-1/2 z-50 max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-lg border border-border bg-background p-6 text-foreground shadow-lg outline-none",
          className,
        )}
        onPointerDownOutside={primaryButtonOnly(onPointerDownOutside)}
        {...props}
      >
        {children}
        <DialogPrimitive.Close asChild>
          <Button
            aria-label="Close dialog"
            className="absolute top-2 right-2 size-11 p-0"
            variant="ghost"
          >
            <X aria-hidden="true" size={20} strokeWidth={1.5} />
          </Button>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}
