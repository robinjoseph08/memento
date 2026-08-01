import { useState } from "react";

import {
  useLoosePreviewRecipients,
  useLooseRecipientPreview,
} from "../hooks/queries/looseItems";
import type { LooseItem } from "../types/generated/events";

export function LooseRecipientPreview({
  looseItem,
  identityGeneration,
  resetKey,
  controlsReady,
}: {
  looseItem: LooseItem;
  identityGeneration: string;
  resetKey: string | number;
  controlsReady: boolean;
}) {
  const [recipientID, setRecipientID] = useState("");
  const recipients = useLoosePreviewRecipients(
    identityGeneration,
    looseItem.id,
    looseItem.audience_complete && controlsReady,
  );
  const selected = recipients.data?.recipients.find(
    (recipient) => recipient.person_id === recipientID,
  );
  return (
    <section
      aria-labelledby="loose-preview-title"
      className="publication-actions"
    >
      <h4 id="loose-preview-title">Preview</h4>
      <label>
        Preview Recipient
        <select
          disabled={recipients.isPending || !controlsReady}
          onChange={(event) => setRecipientID(event.target.value)}
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
      {selected?.access_state !== undefined &&
      selected.access_state !== "completed" ? (
        <p>
          Pending Recipient: cannot access yet. Preview shows approved content
          after Onboarding.
        </p>
      ) : null}
      <LoosePreviewResult
        controlsReady={controlsReady}
        identityGeneration={identityGeneration}
        key={`${resetKey}:${recipientID}`}
        looseItem={looseItem}
        recipientID={recipientID}
      />
    </section>
  );
}

function LoosePreviewResult({
  looseItem,
  identityGeneration,
  recipientID,
  controlsReady,
}: {
  looseItem: LooseItem;
  identityGeneration: string;
  recipientID: string;
  controlsReady: boolean;
}) {
  const [open, setOpen] = useState(false);
  const preview = useLooseRecipientPreview(
    identityGeneration,
    looseItem.id,
    looseItem.version,
    recipientID,
    open,
  );
  return (
    <>
      <button
        disabled={
          !recipientID || !controlsReady || !looseItem.audience_complete
        }
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
              <h5>{preview.data.title || "Untitled Loose item"}</h5>
              <p>1 authorized Media item</p>
              <p>
                {preview.data.media.media_type}
                {preview.data.media.available ? "" : " (unavailable)"}
              </p>
            </>
          ) : null}
          <div aria-label="Disabled Recipient interactions">
            {["Comment", "Favorite", "Settings", "Download"].map((action) => (
              <button disabled key={action} type="button">
                {action}
              </button>
            ))}
          </div>
          <p>Preview activity is not recorded as Recipient engagement.</p>
        </section>
      ) : null}
    </>
  );
}
