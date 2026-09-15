import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

// An Album cover as the top print on a small pile, like the two frames in the
// wordmark. Wrap the link in `group` and the pile fans out a little on hover
// and keyboard focus, which is the one hover cue the tile needs.
export function PrintStack({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <span className={cn("relative block", className)}>
      <span
        aria-hidden="true"
        className="absolute inset-0 -rotate-4 rounded-sm bg-border transition-transform duration-300 group-hover:-rotate-7 group-focus-visible:-rotate-7 motion-reduce:transition-none"
      />
      <span
        aria-hidden="true"
        className="absolute inset-0 rotate-3 rounded-sm bg-accent transition-transform duration-300 group-hover:rotate-5 group-focus-visible:rotate-5 motion-reduce:transition-none"
      />
      <span className="relative block">{children}</span>
    </span>
  );
}
