import { useEffect, useRef, useState, type FormEvent } from "react";

import {
  useCompleteSetup,
  useRequestSetupCode,
  useVerifySetupCode,
} from "./hooks/queries/setup";
import { BrandHeader, ErrorMessage } from "./presentation";
import type { SessionResponse } from "./types/generated/setup";

export function SetupFlow({
  onComplete,
}: {
  onComplete: (session: SessionResponse) => void;
}) {
  const [step, setStep] = useState<"identity" | "code" | "onboarding">(
    "identity",
  );
  const stepHeading = useRef<HTMLHeadingElement>(null);
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [challengeID, setChallengeID] = useState("");
  const [code, setCode] = useState("");
  const [verificationToken, setVerificationToken] = useState("");
  const [privacyAcknowledged, setPrivacyAcknowledged] = useState(false);
  const [engagementAcknowledged, setEngagementAcknowledged] = useState(false);
  const [interestListAcknowledged, setInterestListAcknowledged] =
    useState(false);
  const [emailPreviewsAcknowledged, setEmailPreviewsAcknowledged] =
    useState(false);
  const [pushGuidanceAcknowledged, setPushGuidanceAcknowledged] =
    useState(false);
  const [emailPreference, setEmailPreference] = useState("immediate");
  const [sessionType, setSessionType] = useState("trusted");

  useEffect(() => {
    stepHeading.current?.focus();
  }, [step]);

  const requestCode = useRequestSetupCode((response) => {
    setChallengeID(response.challenge_id);
    setStep("code");
  });
  const verifyCode = useVerifySetupCode((response) => {
    setVerificationToken(response.verification_token);
    setStep("onboarding");
  });
  const completeSetup = useCompleteSetup(onComplete);

  function restartSetup() {
    setChallengeID("");
    setCode("");
    setVerificationToken("");
    requestCode.reset();
    verifyCode.reset();
    completeSetup.reset();
    setStep("identity");
  }

  function submitOnboarding(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    completeSetup.mutate({
      verification_token: verificationToken,
      privacy_acknowledged: privacyAcknowledged,
      engagement_acknowledged: engagementAcknowledged,
      interest_list_acknowledged: interestListAcknowledged,
      email_previews_acknowledged: emailPreviewsAcknowledged,
      push_guidance_acknowledged: pushGuidanceAcknowledged,
      email_preference: emailPreference,
      session_type: sessionType,
    });
  }

  return (
    <section aria-labelledby="memento-title" className="shell-card setup-card">
      <BrandHeader />
      <div className="setup-heading">
        <p className="step-label">
          {step === "identity"
            ? "Step 1 of 3"
            : step === "code"
              ? "Step 2 of 3"
              : "Step 3 of 3"}
        </p>
        <h2 ref={stepHeading} tabIndex={-1}>
          {step === "identity"
            ? "Set up the Curator"
            : step === "code"
              ? "Verify your email"
              : "Choose how Memento works for you"}
        </h2>
      </div>

      {step === "identity" ? (
        <form
          className="setup-form"
          onSubmit={(event) => {
            event.preventDefault();
            requestCode.mutate({ display_name: displayName, email });
          }}
        >
          <p className="form-intro">
            The Person who completes setup receives the sole Curator role and is
            also a Recipient. Complete setup before exposing Memento publicly.
          </p>
          <label>
            Your name
            <input
              autoComplete="name"
              maxLength={120}
              onChange={(event) => setDisplayName(event.target.value)}
              required
              value={displayName}
            />
          </label>
          <label>
            Login email
            <input
              autoComplete="email"
              inputMode="email"
              maxLength={320}
              onChange={(event) => setEmail(event.target.value)}
              required
              type="email"
              value={email}
            />
          </label>
          <ErrorMessage error={requestCode.error} />
          <button disabled={requestCode.isPending} type="submit">
            {requestCode.isPending ? "Sending code…" : "Send verification code"}
          </button>
        </form>
      ) : null}

      {step === "code" ? (
        <form
          className="setup-form"
          onSubmit={(event) => {
            event.preventDefault();
            verifyCode.mutate({ challenge_id: challengeID, code });
          }}
        >
          <p className="form-intro">
            An eight-digit single-use code is queued for delivery to your email.
            It expires ten minutes after the request is accepted.
          </p>
          <label>
            Verification code
            <input
              autoComplete="one-time-code"
              inputMode="numeric"
              maxLength={8}
              minLength={8}
              onChange={(event) => setCode(event.target.value)}
              pattern="[0-9]{8}"
              required
              value={code}
            />
          </label>
          <ErrorMessage error={verifyCode.error ?? requestCode.error} />
          <button disabled={verifyCode.isPending} type="submit">
            {verifyCode.isPending ? "Verifying…" : "Verify email"}
          </button>
          <div className="setup-secondary-actions">
            <button
              disabled={requestCode.isPending}
              onClick={() =>
                requestCode.mutate({ display_name: displayName, email })
              }
              type="button"
            >
              {requestCode.isPending
                ? "Queuing another code…"
                : "Send another code"}
            </button>
            <button onClick={restartSetup} type="button">
              Change name or email
            </button>
          </div>
        </form>
      ) : null}

      {step === "onboarding" ? (
        <form className="setup-form choices" onSubmit={submitOnboarding}>
          <p className="form-intro">
            Your verified email remains valid for thirty minutes. If the
            verification expires, start over and request another code.
          </p>
          <label className="choice">
            <input
              checked={privacyAcknowledged}
              onChange={(event) => setPrivacyAcknowledged(event.target.checked)}
              required
              type="checkbox"
            />
            <span>
              <strong>Private individual access</strong> Each Recipient uses
              their own email and Session. Shared links and public galleries are
              not available.
            </span>
          </label>
          <label className="choice">
            <input
              checked={engagementAcknowledged}
              onChange={(event) =>
                setEngagementAcknowledged(event.target.checked)
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Curator-visible engagement</strong> Meaningful signed-in
              activity is visible to the Curator. Email opens and incidental
              thumbnail loads are not tracked.
            </span>
          </label>
          <label className="choice">
            <input
              checked={interestListAcknowledged}
              onChange={(event) =>
                setInterestListAcknowledged(event.target.checked)
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Interest list starts empty</strong> Choosing People later
              helps propose relevant photos but never grants access.
            </span>
          </label>
          <label className="choice">
            <input
              checked={emailPreviewsAcknowledged}
              onChange={(event) =>
                setEmailPreviewsAcknowledged(event.target.checked)
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Private email previews</strong> Immediate email can
              include one authorized cover, while a weekly digest can include up
              to three. Each embedded preview is a permanent low-resolution
              mailbox copy that can be forwarded and cannot be recalled after
              Withdrawal or Revocation. Messages contain no tracking pixels,
              public Media links, hidden counts, or hidden Moments.
            </span>
          </label>
          <label className="choice">
            <input
              checked={pushGuidanceAcknowledged}
              onChange={(event) =>
                setPushGuidanceAcknowledged(event.target.checked)
              }
              required
              type="checkbox"
            />
            <span>
              <strong>Push is an optional device choice</strong> Supported
              trusted devices can enable limited lock-screen context later.
              Public computers cannot enable push, and Memento never prompts on
              page load.
            </span>
          </label>
          <label>
            Publication and Comment email
            <select
              onChange={(event) => setEmailPreference(event.target.value)}
              value={emailPreference}
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
                checked={sessionType === "trusted"}
                name="session-type"
                onChange={() => setSessionType("trusted")}
                type="radio"
                value="trusted"
              />
              Trusted device, stays signed in while active
            </label>
            <label className="radio-choice">
              <input
                checked={sessionType === "public"}
                name="session-type"
                onChange={() => setSessionType("public")}
                type="radio"
                value="public"
              />
              Public computer, expires within 12 hours
            </label>
          </fieldset>
          <ErrorMessage error={completeSetup.error} />
          <button disabled={completeSetup.isPending} type="submit">
            {completeSetup.isPending ? "Completing setup…" : "Complete setup"}
          </button>
          <div className="setup-secondary-actions">
            <button onClick={restartSetup} type="button">
              Start setup over
            </button>
          </div>
        </form>
      ) : null}
    </section>
  );
}
