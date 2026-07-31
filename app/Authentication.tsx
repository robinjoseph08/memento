import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";

import { APIError } from "./api";
import {
  useAcceptedInvitationSession,
  useAcceptInvitation,
  useConfirmSession,
  useInvitation,
  useRequestSignInCode,
  useVerifySignIn,
} from "./hooks/queries/authentication";
import { formatInvitationExpiry } from "./format";
import { BrandHeader, ErrorMessage } from "./presentation";
import type { SessionResponse } from "./types/generated/setup";
import { useOnlineStatus } from "./useOnlineStatus";
import { OfflineNotice, ThemeToggle } from "./PWAControls";

export function SignInFlow({
  onComplete,
  recovery = false,
}: {
  onComplete: (session: SessionResponse) => void;
  recovery?: boolean;
}) {
  const [email, setEmail] = useState("");
  const [challengeID, setChallengeID] = useState("");
  const [code, setCode] = useState("");
  const [sessionType, setSessionType] = useState("trusted");
  const [verified, setVerified] = useState(false);
  const bootstrap = useConfirmSession(challengeID, verified);
  const requestCode = useRequestSignInCode((response) =>
    setChallengeID(response.challenge_id),
  );
  const verify = useVerifySignIn(() => setVerified(true));

  useEffect(() => {
    if (bootstrap.data) onComplete(bootstrap.data);
  }, [bootstrap.data, onComplete]);

  return (
    <section aria-labelledby="sign-in-title" className="shell-card setup-card">
      <BrandHeader />
      <div className="setup-heading">
        <p className="step-label">
          {recovery ? "Recovery hold" : "Private Recipient access"}
        </p>
        <h2 id="sign-in-title">
          {recovery ? "Curator recovery sign-in" : "Sign in to Memento"}
        </h2>
      </div>
      <p className="lede">
        {recovery
          ? "Recipient access and optional delivery remain blocked until fresh Curator review."
          : "Setup is complete."}
      </p>
      {!challengeID ? (
        <form
          className="setup-form"
          onSubmit={(event) => {
            event.preventDefault();
            requestCode.mutate({ email });
          }}
        >
          <p className="form-intro">
            Enter your login email. Memento does not reveal whether an address
            is eligible and sends a code only when sign-in is available.
            {recovery
              ? " During Recovery hold, only the Curator receives a code."
              : ""}
          </p>
          <label>
            Login email
            <input
              autoComplete="email"
              onChange={(event) => setEmail(event.target.value)}
              required
              type="email"
              value={email}
            />
          </label>
          <ErrorMessage error={requestCode.error} />
          <button disabled={requestCode.isPending} type="submit">
            {requestCode.isPending ? "Requesting…" : "Send sign-in code"}
          </button>
        </form>
      ) : verified ? (
        <div className="setup-form">
          <p className="form-intro">
            Your code was accepted. Loading your Session does not reuse the
            single-use code.
          </p>
          <ErrorMessage error={bootstrap.error} />
          {bootstrap.isError ? (
            <button onClick={() => void bootstrap.refetch()} type="button">
              Retry loading Session
            </button>
          ) : (
            <p role="status">Loading Session…</p>
          )}
        </div>
      ) : (
        <form
          className="setup-form"
          onSubmit={(event) => {
            event.preventDefault();
            verify.mutate({
              challenge_id: challengeID,
              code,
              session_type: sessionType,
            });
          }}
        >
          <p className="form-intro">
            If sign-in is available, the eight-digit single-use code expires in
            ten minutes.
          </p>
          <label>
            Sign-in code
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
          <fieldset>
            <legend>This browser</legend>
            <label className="radio-choice">
              <input
                checked={sessionType === "trusted"}
                name="sign-in-session-type"
                onChange={() => setSessionType("trusted")}
                type="radio"
              />
              Trusted device, expires after one year of inactivity
            </label>
            <label className="radio-choice">
              <input
                checked={sessionType === "public"}
                name="sign-in-session-type"
                onChange={() => setSessionType("public")}
                type="radio"
              />
              Public computer, browser-session cookie and at most 12 hours
            </label>
          </fieldset>
          <ErrorMessage error={verify.error} />
          <button disabled={verify.isPending} type="submit">
            {verify.isPending ? "Signing in…" : "Verify and sign in"}
          </button>
          <div className="setup-secondary-actions">
            <button onClick={() => setChallengeID("")} type="button">
              Use another email
            </button>
          </div>
        </form>
      )}
    </section>
  );
}

export function InvitationLanding({
  onIdentityTransition,
  renderApplication,
}: {
  onIdentityTransition: () => void;
  renderApplication: (session?: SessionResponse) => React.ReactNode;
}) {
  const queryClient = useQueryClient();
  const online = useOnlineStatus();
  const [searchParams] = useSearchParams();
  const [token, setToken] = useState(() => searchParams.get("token") ?? "");
  const [acceptedIdentityGeneration, setAcceptedIdentityGeneration] =
    useState(0);
  const accepted = acceptedIdentityGeneration > 0;
  const invitation = useInvitation(token);
  const accept = useAcceptInvitation(token, () => {
    window.history.replaceState({}, "", "/");
    onIdentityTransition();
    setToken("");
    setAcceptedIdentityGeneration((generation) => generation + 1);
  });
  const acceptedIdentity = useAcceptedInvitationSession(
    acceptedIdentityGeneration,
    online,
  );

  useEffect(() => {
    if (!accepted || online) return;
    void queryClient.cancelQueries({
      predicate: (query) => query.queryKey[0] !== "bootstrap",
    });
    onIdentityTransition();
  }, [accepted, online, onIdentityTransition, queryClient]);

  const currentSession = accepted && online ? acceptedIdentity.data : undefined;
  const acceptedSessionRevoked =
    acceptedIdentity.error instanceof APIError &&
    acceptedIdentity.error.status === 401;
  const invalidInvitation =
    !token ||
    (invitation.error instanceof APIError && invitation.error.status === 404);
  const inspectionUnavailable =
    Boolean(token) && invitation.isError && !invalidInvitation;

  if (currentSession) return renderApplication(currentSession);
  if (acceptedSessionRevoked) return renderApplication();
  if (accepted && !online) {
    return (
      <main>
        <div className="offline-shell">
          <ThemeToggle />
          <OfflineNotice />
        </div>
      </main>
    );
  }

  return (
    <main>
      <section
        aria-labelledby="invitation-title"
        className="shell-card invitation-card"
      >
        <BrandHeader />
        <h2 id="invitation-title">Private Invitation</h2>
        {accepted ? (
          <>
            <p className="lede">Invitation accepted.</p>
            <p>
              Your verified identity is ready for resumable Onboarding. No Media
              access is granted until Onboarding is complete.
            </p>
            {acceptedIdentity.isPending ? (
              <p aria-live="polite">Opening your Onboarding securely…</p>
            ) : null}
            <ErrorMessage error={acceptedIdentity.error} />
            {acceptedIdentity.isError ? (
              <button
                onClick={() => void acceptedIdentity.refetch()}
                type="button"
              >
                Open Onboarding again
              </button>
            ) : null}
          </>
        ) : null}
        {!accepted && token && invitation.isPending ? (
          <p aria-live="polite">Checking this Invitation securely…</p>
        ) : null}
        {!accepted && invalidInvitation ? (
          <p className="form-error" role="alert">
            This Invitation is invalid or no longer available.
          </p>
        ) : null}
        {!accepted && inspectionUnavailable ? (
          <>
            <p className="form-error" role="alert">
              Memento could not check this Invitation. Check your connection and
              try again.
            </p>
            <button onClick={() => void invitation.refetch()} type="button">
              Try again
            </button>
          </>
        ) : null}
        {!accepted && invitation.data ? (
          <>
            <p className="lede">
              {invitation.data.curator_name} invited{" "}
              {invitation.data.recipient_name} to Memento.
            </p>
            <p>
              Memento is a private family photo and video archive. This
              single-use offer expires{" "}
              {formatInvitationExpiry(invitation.data.expires_at)}. Accepting
              starts a verified Onboarding Session but does not grant Media
              access by itself.
            </p>
            <ErrorMessage error={accept.error} />
            <button
              disabled={accept.isPending}
              onClick={() => accept.mutate()}
              type="button"
            >
              {accept.isPending ? "Accepting…" : "Accept Invitation"}
            </button>
          </>
        ) : null}
      </section>
    </main>
  );
}
