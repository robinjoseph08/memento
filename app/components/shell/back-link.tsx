import { ChevronLeft } from "lucide-react";
import type { ReactNode } from "react";
import { Link, type To } from "react-router-dom";

import { cn } from "../../lib/utils";

// The one way back to a parent page, in the viewer and the Curator area
// alike: a chevron and the parent's name, with a quiet hover surface.
export function BackLink({
  to,
  children,
  className,
}: {
  to: To;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Link
      className={cn(
        "mb-6 -ml-2 inline-flex min-h-9 items-center gap-1 rounded-md px-2 text-sm text-muted hover:bg-surface hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring",
        className,
      )}
      to={to}
    >
      <ChevronLeft aria-hidden="true" className="size-4" strokeWidth={1.5} />
      {children}
    </Link>
  );
}
