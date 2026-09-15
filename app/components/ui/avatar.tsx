import { avatarHue, initials } from "@/lib/initials";
import { cn } from "@/lib/utils";
import * as AvatarPrimitive from "@radix-ui/react-avatar";
import type { ComponentProps, CSSProperties } from "react";

export function Avatar({
  className,
  ...props
}: ComponentProps<typeof AvatarPrimitive.Root>) {
  return (
    <AvatarPrimitive.Root
      data-slot="avatar"
      className={cn(
        "relative flex size-8 shrink-0 overflow-hidden rounded-full select-none",
        className,
      )}
      {...props}
    />
  );
}

export function AvatarImage({
  className,
  ...props
}: ComponentProps<typeof AvatarPrimitive.Image>) {
  return (
    <AvatarPrimitive.Image
      data-slot="avatar-image"
      className={cn("size-full object-cover", className)}
      {...props}
    />
  );
}

// With a name, the fallback shows the person's initials on a tint that is
// theirs alone: the hue comes from the name, the lightness from the theme.
export function AvatarFallback({
  className,
  name,
  style,
  children,
  ...props
}: ComponentProps<typeof AvatarPrimitive.Fallback> & { name?: string }) {
  const named = name !== undefined;
  return (
    <AvatarPrimitive.Fallback
      data-slot="avatar-fallback"
      className={cn(
        "flex size-full items-center justify-center rounded-full bg-surface text-sm leading-none font-medium text-foreground",
        named &&
          "bg-[oklch(var(--avatar-lightness)_var(--avatar-chroma)_var(--avatar-hue))] text-[oklch(var(--avatar-text-lightness)_0.07_var(--avatar-hue))]",
        className,
      )}
      style={
        named
          ? ({ ...style, "--avatar-hue": avatarHue(name) } as CSSProperties)
          : style
      }
      {...props}
    >
      {named ? initials(name) : children}
    </AvatarPrimitive.Fallback>
  );
}
