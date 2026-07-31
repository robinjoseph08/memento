import {
  useCompleteOnboarding,
  useOnboarding,
  useSaveOnboarding,
} from "./hooks/queries/onboarding";
import { useRebasedDraft } from "./hooks/useRebasedDraft";
import { BrandHeader, ErrorMessage } from "./presentation";
import { RecipientVisibilityManager } from "./VisibilityManager";
import type { OnboardingRequest } from "./types/generated/recipients";
import type { SessionResponse } from "./types/generated/setup";

export function RecipientOnboarding({
  session,
  onComplete,
  onSignOut,
}: {
  session: SessionResponse;
  onComplete: (session: SessionResponse) => void;
  onSignOut: () => void;
}) {
  const progress = useOnboarding(session.csrf_token);
  const serverDraft: OnboardingRequest | undefined = progress.data
    ? {
        privacy_acknowledged: progress.data.privacy_acknowledged,
        engagement_acknowledged: progress.data.engagement_acknowledged,
        interest_list_acknowledged: progress.data.interest_list_acknowledged,
        email_previews_acknowledged: progress.data.email_previews_acknowledged,
        push_guidance_acknowledged: progress.data.push_guidance_acknowledged,
        email_preference: progress.data.email_preference,
        session_type: progress.data.session_type,
      }
    : undefined;
  const {
    draft,
    setDraft,
    acceptServerDraft,
    hasStaleConflict,
    resetToServer,
  } = useRebasedDraft<OnboardingRequest>(serverDraft);
  const save = useSaveOnboarding(session.csrf_token, (response) =>
    acceptServerDraft({
      privacy_acknowledged: response.privacy_acknowledged,
      engagement_acknowledged: response.engagement_acknowledged,
      interest_list_acknowledged: response.interest_list_acknowledged,
      email_previews_acknowledged: response.email_previews_acknowledged,
      push_guidance_acknowledged: response.push_guidance_acknowledged,
      email_preference: response.email_preference,
      session_type: response.session_type,
    }),
  );
  const complete = useCompleteOnboarding(session, onComplete);
  const busy = save.isPending || complete.isPending;
  const updateDraft = (next: OnboardingRequest) => {
    save.reset();
    setDraft(next);
  };
  const pushSupported =
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window;

  if (progress.isPending)
    return <p aria-live="polite">Restoring your Onboarding choices…</p>;
  if (progress.isError) return <ErrorMessage error={progress.error} />;
  if (!draft) return null;
  return (
    <>
      <section
        aria-labelledby="recipient-onboarding-title"
        className="shell-card setup-card"
      >
        <BrandHeader />
        <p className="step-label">Verified private Invitation</p>
        <h2 id="recipient-onboarding-title">
          Welcome, {progress.data.recipient_name}
        </h2>
        <p className="form-intro">
          Your choices and Interest list are resumable. Media, search, Comments,
          archives, New for you, and optional delivery stay blocked until you
          explicitly complete Onboarding.
        </p>
        <form
          className="setup-form choices"
          onSubmit={(event) => {
            event.preventDefault();
            complete.mutate(draft);
          }}
        >
          <label className="choice">
            <input
              checked={draft.privacy_acknowledged}
              disabled={busy}
              onChange={(event) =>
                updateDraft({
                  ...draft,
                  privacy_acknowledged: event.target.checked,
                })
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Private individual access</strong> Your email and Session
              are personal. There are no public galleries or reusable shared
              Media links.
            </span>
          </label>
          <label className="choice">
            <input
              checked={draft.engagement_acknowledged}
              disabled={busy}
              onChange={(event) =>
                updateDraft({
                  ...draft,
                  engagement_acknowledged: event.target.checked,
                })
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Curator-visible engagement</strong> Meaningful signed-in
              activity is visible to the Curator. Email opens, prefetching, and
              incidental thumbnails are excluded.
            </span>
          </label>
          <label className="choice">
            <input
              checked={draft.interest_list_acknowledged}
              disabled={busy}
              onChange={(event) =>
                updateDraft({
                  ...draft,
                  interest_list_acknowledged: event.target.checked,
                })
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Interest choices suggest, never authorize</strong> Your
              explicit choices below help the Curator prepare Audience
              proposals. The Curator still reviews all access.
            </span>
          </label>
          <label className="choice">
            <input
              checked={draft.email_previews_acknowledged}
              disabled={busy}
              onChange={(event) =>
                updateDraft({
                  ...draft,
                  email_previews_acknowledged: event.target.checked,
                })
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Private email previews</strong> Immediate messages can
              include one authorized cover and weekly digests up to three. Each
              embedded preview is a permanent low-resolution mailbox copy that
              can be forwarded and cannot be recalled after Withdrawal or
              Revocation. Messages contain no tracking pixels, public Media
              links, hidden counts, or hidden Moments.
            </span>
          </label>
          <label>
            Publication and Comment email
            <select
              disabled={busy}
              onChange={(event) =>
                updateDraft({ ...draft, email_preference: event.target.value })
              }
              value={draft.email_preference}
            >
              <option value="immediate">Immediate</option>
              <option value="weekly">Weekly digest</option>
              <option value="none">None</option>
            </select>
          </label>
          <fieldset>
            <legend>This browser</legend>
            <label className="radio-choice">
              <input
                checked={draft.session_type === "trusted"}
                disabled={busy}
                name="recipient-session-type"
                onChange={() =>
                  updateDraft({ ...draft, session_type: "trusted" })
                }
                required
                type="radio"
              />
              Trusted device, stays signed in while active
            </label>
            <label className="radio-choice">
              <input
                checked={draft.session_type === "public"}
                disabled={busy}
                name="recipient-session-type"
                onChange={() =>
                  updateDraft({ ...draft, session_type: "public" })
                }
                required
                type="radio"
              />
              Public computer, expires within 12 hours
            </label>
          </fieldset>
          <label className="choice">
            <input
              checked={draft.push_guidance_acknowledged}
              disabled={busy}
              onChange={(event) =>
                updateDraft({
                  ...draft,
                  push_guidance_acknowledged: event.target.checked,
                })
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Push is optional and device-specific</strong>
              {draft.session_type === "public"
                ? " Public computers cannot enable push."
                : draft.session_type === "trusted"
                  ? pushSupported
                    ? " This device can offer push later from Settings after an explicit action."
                    : " This browser cannot offer push now. On iPhone or iPad, install Memento to the Home Screen first."
                  : " Choose how this browser should be treated to see its guidance."}
              Push can show limited authorized context on a lock screen and is
              independent of email and access.
            </span>
          </label>
          <ErrorMessage error={save.error ?? complete.error} />
          {hasStaleConflict ? (
            <div className="form-error" role="alert">
              <p>
                Your Onboarding choices changed elsewhere while you were
                editing. Your edits are preserved.
              </p>
              <button onClick={resetToServer} type="button">
                Reload latest Onboarding choices
              </button>
            </div>
          ) : null}
          {save.isSuccess ? (
            <p aria-live="polite">Your Onboarding choices were saved.</p>
          ) : null}
          <div className="setup-secondary-actions">
            <button
              disabled={busy}
              onClick={() => save.mutate(draft)}
              type="button"
            >
              {save.isPending ? "Saving…" : "Save and continue later"}
            </button>
            <button disabled={busy} type="submit">
              {complete.isPending
                ? "Completing Onboarding…"
                : "Complete Onboarding"}
            </button>
          </div>
        </form>
      </section>
      <RecipientVisibilityManager onSignOut={onSignOut} session={session} />
    </>
  );
}

export function PublicSessionBanner({
  session,
  onSignOut,
  disabled = false,
}: {
  session: SessionResponse;
  onSignOut: () => void;
  disabled?: boolean;
}) {
  if (session.session_type !== "public") return null;
  return (
    <aside className="public-session-warning">
      <strong>Public computer</strong>
      <span>
        Sign out when finished. Push is disabled, and downloaded originals or
        archives remain on this computer after sign-out.
      </span>
      <button disabled={disabled} onClick={onSignOut} type="button">
        Sign out now
      </button>
    </aside>
  );
}

export function RecipientOnboardingReview() {
  return (
    <details className="recipient-onboarding-review">
      <summary>Review Onboarding</summary>
      <div>
        <h2>Private access and delivery</h2>
        <p>
          Your Session and email are personal. There are no public galleries or
          reusable shared Media links.
        </p>
        <p>
          Meaningful signed-in activity is visible to the Curator. Email opens,
          prefetching, and incidental thumbnails are excluded.
        </p>
        <p>
          Interest-list choices help the Curator prepare Audience proposals but
          never grant access. Email previews can remain in a mailbox after
          access changes, and push can show limited context on a lock screen.
        </p>
      </div>
    </details>
  );
}
