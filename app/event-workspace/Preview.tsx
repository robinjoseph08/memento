import { useState } from "react";

import { formatDateRange } from "../format";
import {
  usePreviewRecipients,
  useRecipientPreview,
} from "../hooks/queries/events";
import type { Event } from "../types/generated/events";

export function RecipientPreview({
  event,
  identityGeneration,
  resetKey,
  saveIsSaved,
}: {
  event: Event;
  identityGeneration: string;
  resetKey: string | number;
  saveIsSaved: boolean;
}) {
  const [recipientID, setRecipientID] = useState("");
  const audiencesComplete =
    event.moments.length > 0 &&
    event.moments.every((moment) => moment.audience_complete);
  const recipients = usePreviewRecipients(
    identityGeneration,
    event.id,
    audiencesComplete,
  );

  return (
    <>
      <label>
        Preview Recipient
        <select
          disabled={recipients.isPending}
          onChange={(input) => setRecipientID(input.target.value)}
          value={recipientID}
        >
          <option value="">Choose a Recipient</option>
          {recipients.data?.recipients.map((recipient) => (
            <option key={recipient.access_id} value={recipient.person_id}>
              {recipient.name} ({recipient.access_state})
            </option>
          ))}
        </select>
      </label>
      {recipients.isPending ? <p>Loading preview Recipients…</p> : null}
      {recipients.isError ? (
        <div className="form-error" role="alert">
          <p>{recipients.error.message}</p>
          <button onClick={() => void recipients.refetch()} type="button">
            Retry Recipient list
          </button>
        </div>
      ) : null}
      {recipients.data?.recipients.length === 0 ? (
        <p>No current Recipients available for preview.</p>
      ) : null}
      {recipients.data?.recipients.some(
        (recipient) =>
          recipient.person_id === recipientID &&
          recipient.access_state !== "completed",
      ) ? (
        <p>
          Pending Recipient: cannot access yet. Preview shows approved content
          after Onboarding.
        </p>
      ) : null}
      <RecipientPreviewResult
        audiencesComplete={audiencesComplete}
        event={event}
        identityGeneration={identityGeneration}
        key={`${resetKey}:${recipientID}`}
        recipientID={recipientID}
        saveIsSaved={saveIsSaved}
      />
    </>
  );
}

function RecipientPreviewResult({
  audiencesComplete,
  event,
  identityGeneration,
  recipientID,
  saveIsSaved,
}: {
  audiencesComplete: boolean;
  event: Event;
  identityGeneration: string;
  recipientID: string;
  saveIsSaved: boolean;
}) {
  const [open, setOpen] = useState(false);
  const preview = useRecipientPreview(
    identityGeneration,
    event.id,
    event.version,
    recipientID,
    open,
  );

  return (
    <>
      <button
        disabled={!recipientID || !saveIsSaved || !audiencesComplete}
        onClick={() => setOpen(true)}
        type="button"
      >
        Preview as Recipient
      </button>
      {open ? (
        <section
          aria-label="Read-only Recipient preview"
          className="recipient-preview"
        >
          <header>
            <strong>Preview as Recipient</strong>
            <span>Read only</span>
          </header>
          {preview.isPending ? <p>Loading preview…</p> : null}
          {preview.isError ? (
            <p className="form-error" role="alert">
              {preview.error.message}
            </p>
          ) : null}
          {preview.data && !preview.data.authorized ? (
            <p>Nothing is shared with this Recipient.</p>
          ) : null}
          {preview.data?.authorized ? (
            <>
              <h5>{preview.data.title}</h5>
              <p>{preview.data.description || "No description"}</p>
              <dl>
                <div>
                  <dt>Date range</dt>
                  <dd>
                    {formatDateRange(
                      preview.data.date_start,
                      preview.data.date_end,
                    )}
                  </dd>
                </div>
                <div>
                  <dt>Place labels</dt>
                  <dd>
                    {preview.data.place_labels?.length
                      ? preview.data.place_labels.join(", ")
                      : "No Place labels"}
                  </dd>
                </div>
                <div>
                  <dt>Cover</dt>
                  <dd>
                    {preview.data.cover_media_id === null
                      ? "No authorized cover"
                      : preview.data.media.some(
                            (item) =>
                              item.id === preview.data.cover_media_id &&
                              item.available,
                          )
                        ? "Authorized cover available"
                        : "Authorized cover unavailable"}
                  </dd>
                </div>
              </dl>
              <p>{preview.data.media_count} authorized Media items</p>
              <ol>
                {preview.data.media.map((item) => (
                  <li key={item.id}>
                    {item.media_type}
                    {item.available ? "" : " (unavailable)"}
                  </li>
                ))}
              </ol>
            </>
          ) : null}
          <div aria-label="Disabled Recipient interactions">
            <button disabled type="button">
              Comment
            </button>
            <button disabled type="button">
              Favorite
            </button>
            <button disabled type="button">
              Settings
            </button>
            <button disabled type="button">
              Download
            </button>
          </div>
          <p>Preview activity is not recorded as Recipient engagement.</p>
        </section>
      ) : null}
    </>
  );
}
