import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

// Balance Epilogue's high-sitting glyphs without moving icon-only buttons using p-0.
const buttonVariants = cva(
  "inline-flex shrink-0 cursor-pointer touch-manipulation [-webkit-tap-highlight-color:transparent] aria-disabled:cursor-default items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-3 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      size: {
        default:
          "min-h-11 px-4 pt-[calc(0.5rem+0.1em)] pb-[calc(0.5rem-0.1em)]",
        sm: "min-h-8 px-3 pt-[calc(0.25rem+0.1em)] pb-[calc(0.25rem-0.1em)] pointer-coarse:min-h-11",
      },
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        outline: "border border-border bg-background hover:bg-surface",
        ghost: "hover:bg-surface",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export function Button({
  className,
  variant,
  size,
  asChild = false,
  type = "button",
  ...props
}: ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      className={cn(buttonVariants({ variant, size, className }))}
      data-slot="button"
      type={asChild ? undefined : type}
      {...props}
    />
  );
}
