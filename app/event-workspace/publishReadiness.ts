import type { Event } from "../types/generated/events";

export type ReadinessCheck = { label: string; done: boolean };

export function calculatePublishReadiness(
  event: Event,
  options: {
    hasUnsavedChanges: boolean;
    metadataValid: boolean;
    publicationPending?: boolean;
    publishedEditableVersion?: number | null;
  },
) {
  const checks: ReadinessCheck[] = [
    { label: "Event details", done: options.metadataValid },
    { label: "Media organization", done: event.unassigned_media.length === 0 },
    {
      label: "Moments",
      done:
        event.moments.length > 0 &&
        event.moments.every((moment) => moment.media_items.length > 0),
    },
    {
      label: "Attendance",
      done:
        event.moments.length > 0 &&
        event.moments.every((moment) => moment.attendance_complete),
    },
    {
      label: "Audiences",
      done:
        event.moments.length > 0 &&
        event.moments.every((moment) => moment.audience_complete),
    },
    { label: "Final review", done: event.final_review_complete },
  ];
  const currentPublication =
    event.lifecycle === "published" &&
    event.staged_update === null &&
    !event.pending_withdrawal_publication &&
    !options.hasUnsavedChanges;
  const nextAction =
    checks.find((check) => !check.done)?.label ??
    (event.pending_withdrawal_publication
      ? "Publish pending Withdrawal restoration"
      : "Ready to publish");
  const editableVersion =
    options.publishedEditableVersion ?? event.published_editable_version;
  const canPublish =
    !options.hasUnsavedChanges &&
    !options.publicationPending &&
    !currentPublication &&
    editableVersion !== event.version &&
    event.unassigned_media.length === 0 &&
    event.moments.length > 0 &&
    event.moments.every((moment) => moment.audience_complete) &&
    event.final_review_complete;

  return { checks, currentPublication, nextAction, canPublish };
}
