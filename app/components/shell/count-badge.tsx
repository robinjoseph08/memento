import { cn } from "../../lib/utils";

// The one way a count sits beside a label: on tabs, section headings, and the
// Requests link. Accent for the thing in focus, attention for what needs a
// decision, muted for the rest. `label` adds hidden words so the accessible
// name says what is being counted.
export function CountBadge({
  count,
  tone = "muted",
  label,
  className,
}: {
  count: number;
  tone?: "accent" | "attention" | "muted";
  label?: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-block rounded-full px-2 text-xs/5 font-medium tabular-nums",
        tone === "accent" && "bg-primary/15 text-accent-foreground",
        tone === "attention" && "bg-destructive/15 text-destructive",
        tone === "muted" && "bg-accent text-muted",
        className,
      )}
    >
      {count}
      {label && <span className="sr-only"> {label}</span>}
    </span>
  );
}
