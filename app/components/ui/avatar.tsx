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

function avatarTint(hue: number): CSSProperties {
  return {
    backgroundColor: `oklch(var(--avatar-lightness) var(--avatar-chroma) ${hue})`,
    color: `oklch(var(--avatar-text-lightness) 0.07 ${hue})`,
  };
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
// The tint is inline so a browser without oklch() keeps the class colors.
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
        className,
      )}
      style={named ? { ...style, ...avatarTint(avatarHue(name)) } : style}
      {...props}
    >
      {named ? initials(name) : children}
    </AvatarPrimitive.Fallback>
  );
}
