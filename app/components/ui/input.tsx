import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

export function Input({ className, type, ...props }: ComponentProps<"input">) {
  return (
    <input
      className={cn(
        "flex min-h-11 w-full min-w-0 rounded-md border border-border bg-background px-3 py-2 text-base text-foreground outline-none placeholder:text-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-3 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-60 aria-invalid:border-destructive md:text-sm",
        className,
      )}
      data-slot="input"
      type={type}
      {...props}
    />
  );
}
