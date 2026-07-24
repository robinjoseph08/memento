import { useQuery } from "@tanstack/react-query";

import type { LiveResponse } from "./types/generated/health";

async function fetchLiveness(): Promise<LiveResponse> {
  const response = await fetch("/api/health/live", {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    throw new Error("Memento is unavailable");
  }
  return (await response.json()) as LiveResponse;
}

// MementoMark mirrors public/icon.svg's three-photo archive geometry.
function MementoMark() {
  return (
    <svg
      aria-label="Memento"
      className="mark"
      role="img"
      viewBox="180 180 664 664"
    >
      <rect
        fill="#0284c7"
        height="440"
        rx="72"
        transform="rotate(-14 437 488)"
        width="340"
        x="267"
        y="268"
      />
      <rect
        fill="#38bdf8"
        height="440"
        rx="72"
        transform="rotate(14 587 488)"
        width="340"
        x="417"
        y="268"
      />
      <rect fill="#bae6fd" height="390" rx="72" width="410" x="307" y="354" />
    </svg>
  );
}

export function App() {
  const liveness = useQuery({
    queryKey: ["health", "live"],
    queryFn: fetchLiveness,
    retry: false,
  });

  const state = liveness.isSuccess
    ? "Process reachable"
    : liveness.isError
      ? "Unavailable"
      : "Starting";

  return (
    <main>
      <section aria-labelledby="memento-title" className="shell-card">
        <MementoMark />
        <p className="eyebrow">PRIVATE FAMILY ARCHIVE</p>
        <h1 id="memento-title">Memento</h1>
        <p className="lede">
          A quiet place for sharing selected photos and videos with family.
        </p>
        <p aria-live="polite" className="status">
          <span aria-hidden="true" className="status-dot" />
          {state}
        </p>
      </section>
    </main>
  );
}
