import { RotateCw, type LucideIcon } from "lucide-react";
import {
  useEffect,
  useId,
  useRef,
  type ComponentProps,
  type ReactNode,
} from "react";

import { focusFirstInvalid } from "../../lib/forms";
import { errorMessage, HTTPError } from "../../lib/http";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

export const headingClass =
  "font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] font-normal tracking-[-1px] text-balance";
export const sectionHeadingClass =
  "font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]";

// A section heading with an icon that says what kind of thing the section
// holds. The icon is decorative; the heading text stays the accessible name.
export function SectionHeading({
  icon: Icon,
  tone = "accent",
  className,
  children,
  ...props
}: ComponentProps<"h2"> & {
  icon: LucideIcon;
  tone?: "accent" | "attention" | "muted";
}) {
  return (
    <h2
      className={cn(sectionHeadingClass, "flex items-center gap-3", className)}
      {...props}
    >
      <Icon
        aria-hidden="true"
        className={cn(
          "relative -top-0.5 size-6 shrink-0",
          tone === "accent" && "text-accent-foreground",
          tone === "attention" && "text-destructive",
          tone === "muted" && "text-muted",
        )}
        strokeWidth={1.5}
      />
      {children}
    </h2>
  );
}

export function Form({
  error,
  children,
  ...props
}: ComponentProps<"form"> & { error: unknown }) {
  const ref = useRef<HTMLFormElement>(null);
  useEffect(() => {
    if (error) focusFirstInvalid(ref.current);
  }, [error]);
  return (
    <form {...props} ref={ref}>
      <Failure error={error} />
      {children}
    </form>
  );
}

export function Failure({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p className="my-4 text-sm text-destructive" role="alert">
      {error instanceof HTTPError && Object.keys(error.fields).length
        ? "Check the highlighted fields."
        : errorMessage(error)}
    </p>
  );
}

export function Field({
  label,
  error,
  ...props
}: ComponentProps<typeof Input> & { label: string; error?: string }) {
  const id = useId();
  return (
    <div className="mb-5">
      <label className="mb-2 block text-xs font-medium" htmlFor={id}>
        {label}
      </label>
      <Input
        {...props}
        aria-describedby={error ? `${id}-error` : undefined}
        aria-invalid={!!error}
        id={id}
      />
      {error && (
        <p className="mt-2 text-xs text-destructive" id={`${id}-error`}>
          {error}
        </p>
      )}
    </div>
  );
}

export function FieldError({ id, error }: { id: string; error?: string }) {
  return error ? (
    <p className="mt-2 text-xs text-destructive" id={id}>
      {error}
    </p>
  ) : null;
}

export function CheckField({
  children,
  error,
  ...props
}: ComponentProps<"input"> & { children: ReactNode; error?: string }) {
  const id = useId();
  return (
    <div className="mb-5">
      <label
        className="flex cursor-pointer items-start gap-3 text-sm"
        htmlFor={id}
      >
        <input
          {...props}
          aria-describedby={error ? `${id}-error` : undefined}
          aria-invalid={!!error}
          className="mt-0.5 size-4 cursor-pointer accent-primary"
          id={id}
          type="checkbox"
        />
        {children}
      </label>
      <FieldError error={error} id={`${id}-error`} />
    </div>
  );
}

export function ReadFailure({
  error,
  retry,
  pending,
}: {
  error: unknown;
  retry: () => unknown;
  pending: boolean;
}) {
  return (
    <div>
      <Failure error={error} />
      <Button disabled={pending} onClick={() => void retry()} variant="outline">
        <RotateCw aria-hidden="true" className="size-4" strokeWidth={1.5} />
        {pending ? "Trying again…" : "Try again"}
      </Button>
    </div>
  );
}
