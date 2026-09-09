import type { ReactNode } from "react";
import { Link, type To } from "react-router-dom";

import { cn } from "../../lib/utils";

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
        "mb-6 inline-block text-sm text-accent-foreground underline underline-offset-4",
        className,
      )}
      to={to}
    >
      {children}
    </Link>
  );
}
