import { useState } from "react";

import { useLooseAudienceReview } from "../hooks/queries/looseAudiences";

const reasonLabels: Record<string, string> = {
  manually_included: "Manually included",
  manually_excluded: "Manually excluded",
};

export function LooseAudienceReview({
  looseItemID,
  identityGeneration,
  selectionGeneration,
  onReviewChanged,
  disabled = false,
}: {
  looseItemID: string;
  identityGeneration: string;
  selectionGeneration: number;
  disabled?: boolean;
  onReviewChanged: (
    kind: "audience-changed" | "audience-approved",
    looseItemID: string,
    selectionGeneration: number,
  ) => void;
}) {
  const [manualRecipientID, setManualRecipientID] = useState("");
  const audience = useLooseAudienceReview(identityGeneration, looseItemID, {
    onAudienceChanged: () =>
      onReviewChanged("audience-changed", looseItemID, selectionGeneration),
    onAudienceApproved: () =>
      onReviewChanged("audience-approved", looseItemID, selectionGeneration),
  });
  const { review, override, recalculate, approve } = audience;
  if (review.isPending) return <p>Loading Audience…</p>;
  if (review.isError || !review.data)
    return (
      <div className="form-error" role="alert">
        <p>{review.error?.message ?? "The Audience could not be loaded."}</p>
        <button onClick={() => void review.refetch()} type="button">
          Retry Audience
        </button>
      </div>
    );
  const busy =
    disabled ||
    override.isPending ||
    recalculate.isPending ||
    approve.isPending;
  return (
    <section
      aria-labelledby="loose-audience-title"
      className="attendance-audience-review"
    >
      <h3 id="loose-audience-title">Audience</h3>
      <p>
        Loose items have no Attendance. Choose Eligible Recipients directly,
        then approve an immutable Audience snapshot.
      </p>
      {audience.errors.length ? (
        <div className="form-error" role="alert">
          <p>{audience.errors.at(-1)?.message}</p>
          {audience.hasConflict ? (
            <button onClick={() => void audience.reset()} type="button">
              Load latest Audience
            </button>
          ) : null}
        </div>
      ) : null}
      <h4>Audience proposal</h4>
      {review.data.proposal.length === 0 ? (
        <p>
          No Recipients proposed. Approve the explicit empty Audience to keep
          this Loose item Curator only.
        </p>
      ) : (
        <ul className="proposal-list">
          {review.data.proposal.map((proposal) => (
            <li key={proposal.recipient.id}>
              <label>
                <input
                  checked={proposal.included}
                  disabled={busy}
                  onChange={(event) =>
                    override.mutate({
                      recipient_person_id: proposal.recipient.id,
                      state: event.target.checked ? "included" : "excluded",
                    })
                  }
                  type="checkbox"
                />
                {proposal.recipient.display_name}
              </label>
              {proposal.reasons.length ? (
                <ul>
                  {proposal.reasons.map((reason, index) => (
                    <li key={`${reason.kind}-${index}`}>
                      {reasonLabels[reason.kind] ?? reason.kind}
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      <div className="move-control">
        <label>
          Manually include Recipient
          <select
            disabled={busy}
            onChange={(event) => setManualRecipientID(event.target.value)}
            value={manualRecipientID}
          >
            <option value="">Choose an Eligible Recipient</option>
            {review.data.eligible_recipients.map((recipient) => (
              <option key={recipient.id} value={recipient.id}>
                {recipient.display_name}
              </option>
            ))}
          </select>
        </label>
        <button
          disabled={busy || !manualRecipientID}
          onClick={() =>
            override.mutate({
              recipient_person_id: manualRecipientID,
              state: "included",
            })
          }
          type="button"
        >
          Include Recipient
        </button>
      </div>
      <div className="moment-actions">
        <button
          disabled={busy}
          onClick={() => recalculate.mutate()}
          type="button"
        >
          Recalculate proposal
        </button>
        <button disabled={busy} onClick={() => approve.mutate()} type="button">
          {review.data.proposal.some((proposal) => proposal.included)
            ? "Approve Audience"
            : "Approve Curator only"}
        </button>
      </div>
      {review.data.approved_audience ? (
        <p>
          <strong>
            {review.data.audience_complete
              ? "Approved snapshot:"
              : "Previous approved snapshot:"}
          </strong>{" "}
          {review.data.approved_audience.label} (
          {review.data.approved_audience.recipients.length} Recipients). It will
          not recalculate later.
          {!review.data.audience_complete
            ? " Review and approve the current proposal."
            : ""}
        </p>
      ) : (
        <p>No Audience approved yet.</p>
      )}
    </section>
  );
}
