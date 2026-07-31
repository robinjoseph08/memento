import { calculatePublishReadiness } from "./publishReadiness";
import type { Event } from "../types/generated/events";

export function Readiness({
  event,
  hasUnsavedChanges,
  metadataValid,
}: {
  event: Event;
  hasUnsavedChanges: boolean;
  metadataValid: boolean;
}) {
  const { checks, currentPublication, nextAction } = calculatePublishReadiness(
    event,
    { hasUnsavedChanges, metadataValid },
  );
  const complete = checks.filter((check) => check.done).length;
  return (
    <section aria-labelledby="readiness-title" className="readiness">
      <h3 id="readiness-title">Readiness</h3>
      <p>
        {complete} of {checks.length} complete
      </p>
      <progress
        aria-label="Draft progress"
        max={checks.length}
        value={complete}
      />
      <ul>
        {checks.map((check) => (
          <li key={check.label}>
            <span aria-hidden="true">{check.done ? "✓" : "○"}</span>{" "}
            {check.label}
          </li>
        ))}
      </ul>
      <p>
        <strong>
          {currentPublication ? "Publication status:" : "Next action:"}
        </strong>{" "}
        {currentPublication ? "Published and up to date" : nextAction}
      </p>
    </section>
  );
}
