import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "../../lib/utils";
import { sectionHeadingClass } from "../people/form-fields";

// An empty screen is an invitation to act: an icon that says what would live
// here, a heading, one line of direction, and the action when there is one.
// Without a title it is a single quiet row for a section that already has a
// heading of its own.
export function EmptyState({
  icon: Icon,
  title,
  action,
  className,
  children,
  ...props
}: {
  icon: LucideIcon;
  title?: string;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
  id?: string;
  role?: string;
}) {
  if (!title)
    return (
      <div
        className={cn(
          "flex items-center gap-4 rounded-xl bg-surface px-5 py-5 text-sm text-muted",
          className,
        )}
        {...props}
      >
        <span
          aria-hidden="true"
          className="flex size-10 shrink-0 items-center justify-center rounded-full bg-accent text-accent-foreground"
        >
          <Icon className="size-5" strokeWidth={1.5} />
        </span>
        <span className="min-w-0">{children}</span>
      </div>
    );
  return (
    <section
      className={cn(
        "flex flex-col items-center rounded-2xl bg-surface px-6 py-12 text-center",
        className,
      )}
      {...props}
    >
      <span
        aria-hidden="true"
        className="flex size-16 items-center justify-center rounded-full bg-accent text-accent-foreground"
      >
        <Icon className="size-8" strokeWidth={1.25} />
      </span>
      <h2 className={cn(sectionHeadingClass, "mt-5 text-balance")}>{title}</h2>
      <p className="mt-3 max-w-md text-pretty text-muted">{children}</p>
      {action && <div className="mt-6 flex flex-wrap gap-3">{action}</div>}
    </section>
  );
}
