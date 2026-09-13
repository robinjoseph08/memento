// Counts pending Access Requests beside the Requests link. The hidden text
// keeps the link's accessible name meaningful.
export function PendingBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <span className="rounded-sm bg-primary/15 px-1.5 text-xs text-accent-foreground">
      {count}
      <span className="sr-only">{" pending"}</span>
    </span>
  );
}
