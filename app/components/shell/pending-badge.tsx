// Counts pending Access Requests beside the Requests link. The hidden text
// keeps the link's accessible name meaningful.
export function PendingBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <span className="inline-flex min-w-5 items-center justify-center rounded-full bg-primary px-1.5 text-xs font-medium text-primary-foreground">
      {count}
      <span className="sr-only">{" pending"}</span>
    </span>
  );
}
