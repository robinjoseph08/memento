import { useEffect, useState } from "react";

import { usePublishEvent } from "../hooks/queries/events";
import { calculatePublishReadiness } from "./publishReadiness";
import { RecipientPreview } from "./Preview";
import type { Event, PublicationResponse } from "../types/generated/events";
import type { PublishEventAttempt } from "../hooks/queries/events";

export function Publication({
  event,
  identityGeneration,
  revision,
  selectionGeneration,
  resetKey,
  previewResetKey,
  saveIsSaved,
  metadataValid,
  onPublished,
  onAuthorityUncertain,
  onBusyChange,
}: {
  event: Event;
  identityGeneration: string;
  revision: number;
  selectionGeneration: number;
  resetKey: string | number;
  previewResetKey: string | number;
  saveIsSaved: boolean;
  metadataValid: boolean;
  onPublished: (
    publication: PublicationResponse,
    attempt: PublishEventAttempt,
    authoritativeEvent: Event | undefined,
  ) => void;
  onAuthorityUncertain: (attempt: PublishEventAttempt) => void;
  onBusyChange: (busy: boolean) => void;
}) {
  return (
    <PublicationState
      event={event}
      identityGeneration={identityGeneration}
      key={resetKey}
      metadataValid={metadataValid}
      onAuthorityUncertain={onAuthorityUncertain}
      onBusyChange={onBusyChange}
      onPublished={onPublished}
      previewResetKey={previewResetKey}
      revision={revision}
      saveIsSaved={saveIsSaved}
      selectionGeneration={selectionGeneration}
    />
  );
}

function PublicationState({
  event,
  identityGeneration,
  revision,
  selectionGeneration,
  previewResetKey,
  saveIsSaved,
  metadataValid,
  onPublished,
  onAuthorityUncertain,
  onBusyChange,
}: Omit<Parameters<typeof Publication>[0], "resetKey">) {
  const [notifyRecipients, setNotifyRecipients] = useState(true);
  const [previewReset, setPreviewReset] = useState(0);
  const publish = usePublishEvent(identityGeneration, {
    onStarted: () => {
      onBusyChange(true);
      setPreviewReset((current) => current + 1);
    },
    onCommitted: () => undefined,
    onSuccess: (publication, attempt, authoritativeEvent) =>
      onPublished(publication, attempt, authoritativeEvent),
    onError: (_error, attempt) => onAuthorityUncertain(attempt),
  });

  useEffect(() => {
    onBusyChange(publish.isPending);
    return () => onBusyChange(false);
  }, [onBusyChange, publish.isPending]);

  const readiness = calculatePublishReadiness(event, {
    hasUnsavedChanges: !saveIsSaved,
    metadataValid,
    publicationPending: publish.isPending,
    publishedEditableVersion: publish.data?.editable_version,
  });

  return (
    <section
      aria-labelledby="publication-actions-title"
      className="publication-actions"
    >
      <h4 id="publication-actions-title">Publication</h4>
      {event.published_attendance_recovery_required ? (
        <p className="form-error" role="alert">
          Person search is unavailable for this existing Publication because its
          Attendance cannot be reconstructed safely. Review and publish the
          Event again to restore it.
        </p>
      ) : null}
      <label>
        <input
          checked={notifyRecipients}
          onChange={(input) => setNotifyRecipients(input.target.checked)}
          type="checkbox"
        />
        Include notification intent
      </label>
      <button
        disabled={!readiness.canPublish}
        onClick={() =>
          publish.mutate({
            event,
            revision,
            selectionGeneration,
            notifyRecipients,
          })
        }
        type="button"
      >
        {publish.isPending ? "Publishing…" : "Publish Event"}
      </button>
      {publish.isError ? (
        <p className="form-error" role="alert">
          {publish.error.message}
        </p>
      ) : null}
      {publish.data ? (
        <p role="status">
          Published revision {publish.data.revision} atomically.
        </p>
      ) : null}
      <RecipientPreview
        event={event}
        identityGeneration={identityGeneration}
        resetKey={`${previewResetKey}:${previewReset}`}
        saveIsSaved={saveIsSaved && !publish.isPending}
      />
    </section>
  );
}
