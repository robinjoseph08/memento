import { CountBadge } from "./count-badge";

// Counts pending Access Requests beside the Requests link. The hidden text
// and the leading space keep the link's accessible name "Requests 1 pending".
export function PendingBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <>
      {" "}
      <CountBadge count={count} label="pending" tone="accent" />
    </>
  );
}
