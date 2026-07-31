import { AttendanceAudienceReview } from "../AttendanceAudienceReview";
import type { Event, Moment } from "../types/generated/events";

export function AudienceInspection({
  event,
  inspectedMoment,
  identityGeneration,
  selectionGeneration,
  onReviewChanged,
  onEventChange,
}: {
  event: Event;
  inspectedMoment: Moment | undefined;
  identityGeneration: string;
  selectionGeneration: number;
  onReviewChanged: (
    kind: "attendance-confirmed" | "audience-changed" | "audience-approved",
    momentID: string,
    selectionGeneration: number,
  ) => void;
  onEventChange: (mutator: (next: Event) => void) => void;
}) {
  return (
    <>
      <h3>Attendance and Audience</h3>
      {!inspectedMoment ? (
        <p>Choose a Moment to inspect.</p>
      ) : (
        <>
          <p>{inspectedMoment.title || inspectedMoment.proposed_day}</p>
          <AttendanceAudienceReview
            key={inspectedMoment.id}
            csrfToken={identityGeneration}
            momentID={inspectedMoment.id}
            onAttendanceConfirmed={() =>
              onReviewChanged(
                "attendance-confirmed",
                inspectedMoment.id,
                selectionGeneration,
              )
            }
            onAudienceChanged={() =>
              onReviewChanged(
                "audience-changed",
                inspectedMoment.id,
                selectionGeneration,
              )
            }
            onAudienceApproved={() =>
              onReviewChanged(
                "audience-approved",
                inspectedMoment.id,
                selectionGeneration,
              )
            }
          />
          <p>
            {inspectedMoment.media_items.length} Media items in this Moment.
          </p>
        </>
      )}
      <label className="inspection-check final-review">
        <input
          checked={event.final_review_complete}
          onChange={(input) =>
            onEventChange((next) => {
              next.final_review_complete = input.target.checked;
            })
          }
          type="checkbox"
        />
        Final review complete
      </label>
    </>
  );
}
