import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";

import type {
  AvailabilityResponse,
  CompleteRequest,
  CompleteResponse,
  RequestCodeRequest,
  RequestCodeResponse,
  SessionResponse,
  VerifyCodeRequest,
  VerifyCodeResponse,
} from "./types/generated/setup";

type BootstrapState =
  | { kind: "available" }
  | { kind: "session"; session: SessionResponse }
  | { kind: "closed" };

class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function apiResponse(path: string, init?: RequestInit) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (response.ok) {
    return response;
  }
  let message = "Memento is unavailable.";
  try {
    const payload = (await response.json()) as {
      error?: { message?: string };
    };
    if (payload.error?.message) {
      message = payload.error.message;
    }
  } catch {
    // The safe fallback does not expose response internals.
  }
  throw new APIError(message, response.status);
}

async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await apiResponse(path, init);
  return (await response.json()) as T;
}

async function apiNoContent(path: string, init: RequestInit): Promise<void> {
  await apiResponse(path, init);
}

async function fetchBootstrap(): Promise<BootstrapState> {
  try {
    await apiJSON<AvailabilityResponse>("/api/setup");
    return { kind: "available" };
  } catch (error) {
    if (!(error instanceof APIError) || error.status !== 404) {
      throw error;
    }
  }

  try {
    const session = await apiJSON<SessionResponse>("/api/session");
    if (session.session_type === "trusted") {
      await apiNoContent("/api/session/refresh", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      });
    }
    return { kind: "session", session };
  } catch (error) {
    if (error instanceof APIError && error.status === 401) {
      return { kind: "closed" };
    }
    throw error;
  }
}

function MementoMark() {
  return (
    <svg
      aria-label="Memento"
      className="mark"
      role="img"
      viewBox="180 180 664 664"
    >
      <rect
        fill="#0284c7"
        height="440"
        rx="72"
        transform="rotate(-14 437 488)"
        width="340"
        x="267"
        y="268"
      />
      <rect
        fill="#38bdf8"
        height="440"
        rx="72"
        transform="rotate(14 587 488)"
        width="340"
        x="417"
        y="268"
      />
      <rect fill="#bae6fd" height="390" rx="72" width="410" x="307" y="354" />
    </svg>
  );
}

function BrandHeader() {
  return (
    <>
      <MementoMark />
      <p className="eyebrow">PRIVATE FAMILY ARCHIVE</p>
      <h1 id="memento-title">Memento</h1>
    </>
  );
}

function ErrorMessage({ error }: { error: Error | null }) {
  if (!error) {
    return null;
  }
  return (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  );
}

function SetupFlow({
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
  const [emailPreference, setEmailPreference] = useState("immediate");
  const [sessionType, setSessionType] = useState("trusted");

  useEffect(() => {
    stepHeading.current?.focus();
  }, [step]);

  const requestCode = useMutation({
    mutationFn: (request: RequestCodeRequest) =>
      apiJSON<RequestCodeResponse>("/api/setup/code", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess: (response) => {
      setChallengeID(response.challenge_id);
      setStep("code");
    },
  });

  const verifyCode = useMutation({
    mutationFn: (request: VerifyCodeRequest) =>
      apiJSON<VerifyCodeResponse>("/api/setup/verify", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess: (response) => {
      setVerificationToken(response.verification_token);
      setStep("onboarding");
    },
  });

  const completeSetup = useMutation({
    mutationFn: async (request: CompleteRequest) => {
      const completed = await apiJSON<CompleteResponse>("/api/setup/complete", {
        method: "POST",
        body: JSON.stringify(request),
      });
      const session = await apiJSON<SessionResponse>("/api/session");
      if (session.csrf_token !== completed.csrf_token) {
        throw new APIError("The new Session could not be confirmed.", 500);
      }
      return session;
    },
    onSuccess: onComplete,
  });

  function submitIdentity(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    requestCode.mutate({ display_name: displayName, email });
  }

  function submitCode(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    verifyCode.mutate({ challenge_id: challengeID, code });
  }

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
        <form className="setup-form" onSubmit={submitIdentity}>
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
        <form className="setup-form" onSubmit={submitCode}>
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
            Your verified setup link remains valid for thirty minutes. If it
            expires, start over and request another code.
          </p>
          <label className="choice">
            <input
              checked={privacyAcknowledged}
              onChange={(event) => setPrivacyAcknowledged(event.target.checked)}
              required
              type="checkbox"
            />
            <span>
              <strong>Private individual access</strong>
              Each Recipient uses their own email and Session. Shared links and
              public galleries are not available.
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
              <strong>Curator-visible engagement</strong>
              Meaningful signed-in activity is visible to the Curator. Email
              opens and incidental thumbnail loads are not tracked.
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
              <strong>Interest list starts empty</strong>
              Choosing People later helps propose relevant photos but never
              grants access.
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

function ReadyCard({ session }: { session?: SessionResponse }) {
  return (
    <section aria-labelledby="memento-title" className="shell-card">
      <BrandHeader />
      <p className="lede">
        {session
          ? `Setup is complete. You're signed in as ${session.display_name}.`
          : "Setup is complete. Memento is ready for private family sharing."}
      </p>
      <p aria-live="polite" className="status">
        <span aria-hidden="true" className="status-dot" />
        Setup complete
      </p>
    </section>
  );
}

export function App() {
  const [completedSession, setCompletedSession] = useState<SessionResponse>();
  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: fetchBootstrap,
    retry: false,
  });

  if (completedSession) {
    return (
      <main>
        <ReadyCard session={completedSession} />
      </main>
    );
  }

  if (bootstrap.isPending) {
    return (
      <main>
        <section aria-labelledby="memento-title" className="shell-card">
          <BrandHeader />
          <p aria-live="polite" className="status">
            <span aria-hidden="true" className="status-dot" />
            Loading securely
          </p>
        </section>
      </main>
    );
  }

  if (bootstrap.isError) {
    return (
      <main>
        <section aria-labelledby="memento-title" className="shell-card">
          <BrandHeader />
          <p className="lede" role="alert">
            Memento is unavailable.
          </p>
        </section>
      </main>
    );
  }

  if (bootstrap.data.kind === "available") {
    return (
      <main>
        <SetupFlow onComplete={setCompletedSession} />
      </main>
    );
  }

  const session =
    bootstrap.data.kind === "session" ? bootstrap.data.session : undefined;
  return (
    <main>
      <ReadyCard session={session} />
    </main>
  );
}
