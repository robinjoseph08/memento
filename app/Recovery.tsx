import { useState } from "react";

import { SignInFlow } from "./Authentication";
import {
  useRecoveryReview,
  useReleaseRecovery,
} from "./hooks/queries/recovery";
import { BrandHeader, ErrorMessage } from "./presentation";
import type { SessionResponse } from "./types/generated/setup";

export function RecoveryCard({
  session,
  onComplete,
  onReleased,
  confirmingRelease = false,
  confirmationPending = false,
  confirmationError = null,
  onRetryConfirmation,
}: {
  session?: SessionResponse;
  onComplete: (session: SessionResponse) => void;
  onReleased: () => void;
  confirmingRelease?: boolean;
  confirmationPending?: boolean;
  confirmationError?: Error | null;
  onRetryConfirmation?: () => void;
}) {
  const [reviewed, setReviewed] = useState(false);
  const review = useRecoveryReview(session);
  const release = useReleaseRecovery(session, onReleased);
  const restoredCounts = review.data
    ? ([
        ["People", review.data.counts.people],
        ["Current Recipients", review.data.counts.current_recipients],
        ["Completed Recipients", review.data.counts.completed_recipients],
        ["Suspended Recipients", review.data.counts.suspended_recipients],
        ["Revoked access generations", review.data.counts.revoked_generations],
        ["Invalidated restored Sessions", review.data.counts.restored_sessions],
        ["Fresh Sessions", review.data.counts.fresh_sessions],
        ["Audience entitlements", review.data.counts.audience_entitlements],
        ["Published Events", review.data.counts.published_events],
        ["Published Media items", review.data.counts.published_media_items],
        ["Active Withdrawals", review.data.counts.active_withdrawals],
        [
          "Pending optional email batches",
          review.data.counts.pending_email_batches,
        ],
        [
          "Active Push subscriptions",
          review.data.counts.active_push_subscriptions,
        ],
      ] as const)
    : [];

  if (confirmingRelease) {
    return (
      <section className="shell-card setup-card">
        <BrandHeader />
        <h2>Confirming Recovery release</h2>
        {confirmationPending ? (
          <p role="status">Restoring normal access securely…</p>
        ) : null}
        {confirmationError ? (
          <>
            <ErrorMessage error={confirmationError} />
            <button onClick={onRetryConfirmation} type="button">
              Retry confirming Recovery release
            </button>
          </>
        ) : null}
      </section>
    );
  }

  if (!session) return <SignInFlow onComplete={onComplete} recovery />;
  if (!session.curator) {
    return (
      <section className="shell-card setup-card">
        <BrandHeader />
        <h2>Fresh Curator review required</h2>
        <p role="alert">
          This Session cannot review or release Recovery hold. Sign in with the
          Curator login email and its fresh code.
        </p>
      </section>
    );
  }
  return (
    <section aria-labelledby="recovery-title" className="shell-card setup-card">
      <BrandHeader />
      <div className="setup-heading">
        <p className="step-label">Recovery hold</p>
        <h2 id="recovery-title">Review restored authorization state</h2>
      </div>
      <p className="lede">
        Recipient access, restored Sessions, optional email, Web Push, and
        delivery work remain blocked.
      </p>
      {review.isPending ? <p role="status">Loading restored state…</p> : null}
      <ErrorMessage error={review.error ?? release.error} />
      {review.data ? (
        <>
          <p>
            Recovery hold began{" "}
            {new Date(review.data.started_at).toLocaleString()}.
          </p>
          <dl className="recovery-counts">
            {restoredCounts.map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <label className="checkbox-choice">
            <input
              checked={reviewed}
              onChange={(event) => setReviewed(event.target.checked)}
              type="checkbox"
            />
            I reviewed the restored Recipient access, Sessions, Audiences, and
            delivery state and want to release Recovery hold.
          </label>
          <button
            className="recovery-release"
            disabled={!reviewed || release.isPending}
            onClick={() => release.mutate()}
            type="button"
          >
            {release.isPending ? "Releasing…" : "Release Recovery hold"}
          </button>
        </>
      ) : null}
    </section>
  );
}
