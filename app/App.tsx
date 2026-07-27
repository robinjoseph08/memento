import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";

import { APIError, apiJSON, apiNoContent } from "./api";
import { EventOrganizer } from "./EventOrganizer";
import { FamilyManager } from "./FamilyManager";
import { InvitationSuggestions } from "./InvitationSuggestions";
import { PeopleManager } from "./PeopleManager";
import { RepairWorkspace } from "./RepairWorkspace";
import {
  RecipientVisibilityManager,
  VisibilityManager,
} from "./VisibilityManager";
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
import type {
  AcceptResponse,
  InspectResponse,
  OnboardingCompleteResponse,
  OnboardingRequest,
  OnboardingResponse,
} from "./types/generated/recipients";
import type {
  EmailChangeCompleteRequest,
  EmailChangeResponse,
  EmailChangeStartResponse,
  ListResponse as SessionListResponse,
  SignInStartResponse,
  SignInVerifyRequest,
} from "./types/generated/sessions";
import type {
  Album as SourceAlbum,
  DiscoveryResponse,
  ListResponse as SourceListResponse,
  ReconciliationResponse,
} from "./types/generated/sources";

type BootstrapState =
  | { kind: "available" }
  | { kind: "session"; session: SessionResponse }
  | { kind: "closed" };

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

function SignInFlow({
  onComplete,
}: {
  onComplete: (session: SessionResponse) => void;
}) {
  const [email, setEmail] = useState("");
  const [challengeID, setChallengeID] = useState("");
  const [code, setCode] = useState("");
  const [sessionType, setSessionType] = useState("trusted");
  const [verified, setVerified] = useState(false);
  const requestCode = useMutation({
    mutationFn: () =>
      apiJSON<SignInStartResponse>("/api/auth/sign-in/request", {
        method: "POST",
        body: JSON.stringify({ email }),
      }),
    onSuccess: (response) => setChallengeID(response.challenge_id),
  });
  const bootstrap = useMutation({
    mutationFn: () => apiJSON<SessionResponse>("/api/session"),
    onSuccess: onComplete,
  });
  const verify = useMutation({
    mutationFn: (request: SignInVerifyRequest) =>
      apiJSON("/api/auth/sign-in/verify", {
        method: "POST",
        body: JSON.stringify(request),
      }),
    onSuccess: () => {
      setVerified(true);
      bootstrap.mutate();
    },
  });
  return (
    <section aria-labelledby="sign-in-title" className="shell-card setup-card">
      <BrandHeader />
      <div className="setup-heading">
        <p className="step-label">Private Recipient access</p>
        <h2 id="sign-in-title">Sign in to Memento</h2>
      </div>
      <p className="lede">Setup is complete.</p>
      {!challengeID ? (
        <form
          className="setup-form"
          onSubmit={(event) => {
            event.preventDefault();
            requestCode.mutate();
          }}
        >
          <p className="form-intro">
            Enter your login email. Memento does not reveal whether an address
            is eligible and sends a code only when sign-in is available.
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
            <button onClick={() => bootstrap.mutate()} type="button">
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
  const [emailPreviewsAcknowledged, setEmailPreviewsAcknowledged] =
    useState(false);
  const [pushGuidanceAcknowledged, setPushGuidanceAcknowledged] =
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
              <strong>Private email previews</strong>
              Immediate email can include one authorized cover, while a weekly
              digest can include up to three. Each embedded preview is a
              permanent low-resolution mailbox copy that can be forwarded and
              cannot be recalled after Withdrawal or Revocation. Messages
              contain no tracking pixels, public Media links, hidden counts, or
              hidden Moments.
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
              <strong>Push is an optional device choice</strong>
              Supported trusted devices can enable limited lock-screen context
              later. Public computers cannot enable push, and Memento never
              prompts on page load.
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

function formatSourceDate(value: unknown) {
  if (typeof value !== "string") {
    return "Unknown";
  }
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "Unknown"
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(date);
}

function formatInvitationExpiry(value: unknown) {
  if (typeof value !== "string") {
    return "an unknown time";
  }
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "an unknown time"
    : new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "long",
      }).format(date);
}

function SourceAlbumCard({
  album,
  csrfToken,
  onTriaged,
}: {
  album: SourceAlbum;
  csrfToken: string;
  onTriaged: (message: string) => void;
}) {
  const queryClient = useQueryClient();
  const [inspecting, setInspecting] = useState(false);
  const triageMutation = useMutation({
    mutationFn: () =>
      apiJSON<SourceAlbum>(
        `/api/sources/${album.id}/${album.disposition === "ignored" ? "restore" : "ignore"}`,
        {
          method: "POST",
          headers: {
            "If-Match": `"${album.version}"`,
            "X-Memento-CSRF": csrfToken,
          },
        },
      ),
    onSuccess: async () => {
      onTriaged(
        album.disposition === "ignored"
          ? `Restored ${album.name} to the Source album inbox.`
          : `Ignored ${album.name}.`,
      );
      await queryClient.invalidateQueries({ queryKey: ["sources"] });
    },
  });
  const reconciliation = useMutation({
    mutationFn: () =>
      apiJSON<ReconciliationResponse>(`/api/sources/${album.id}/reconcile`, {
        method: "POST",
        headers: { "X-Memento-CSRF": csrfToken },
      }),
    onSuccess: () => {
      onTriaged(`Queued reconciliation for ${album.name}.`);
    },
  });
  const detailsID = `source-album-${album.id}-details`;
  return (
    <article className="source-album">
      <div className="source-album-summary">
        <div>
          <h3>{album.name}</h3>
          <p>
            {album.asset_count} {album.asset_count === 1 ? "item" : "items"}
            {album.source_missing ? " · Source missing" : ""}
          </p>
        </div>
        <button
          aria-controls={detailsID}
          aria-expanded={inspecting}
          aria-label={`${inspecting ? "Close details for" : "Inspect"} ${album.name}`}
          onClick={() => setInspecting((value) => !value)}
          type="button"
        >
          {inspecting ? "Close" : "Inspect"}
        </button>
      </div>
      {inspecting ? (
        <div className="source-details" id={detailsID}>
          <p>{album.description || "No source description."}</p>
          <dl>
            <div>
              <dt>Source updated</dt>
              <dd>{formatSourceDate(album.source_updated_at)}</dd>
            </div>
            <div>
              <dt>Last seen</dt>
              <dd>{formatSourceDate(album.last_seen_at)}</dd>
            </div>
          </dl>
          <ErrorMessage error={reconciliation.error ?? triageMutation.error} />
          <div className="source-details-actions">
            <button
              className="source-reconcile-action"
              disabled={reconciliation.isPending || triageMutation.isPending}
              onClick={() => reconciliation.mutate()}
              type="button"
            >
              {reconciliation.isPending ? "Queueing…" : "Reconcile now"}
            </button>
            <button
              className="source-primary-action"
              disabled={triageMutation.isPending || reconciliation.isPending}
              onClick={() => triageMutation.mutate()}
              type="button"
            >
              {triageMutation.isPending
                ? "Saving…"
                : album.disposition === "ignored"
                  ? "Restore to inbox"
                  : "Ignore Source album"}
            </button>
          </div>
        </div>
      ) : null}
    </article>
  );
}

function SourceWorkspace({
  session,
  onSignOut,
  signOutError,
  signOutPending,
}: {
  session: SessionResponse;
  onSignOut: () => void;
  signOutError: Error | null;
  signOutPending: boolean;
}) {
  const queryClient = useQueryClient();
  const [triageStatus, setTriageStatus] = useState("");
  const [searchParams, setSearchParams] = useSearchParams();
  const disposition =
    searchParams.get("source_view") === "ignored" ? "ignored" : "unreviewed";
  const selectDisposition = (nextDisposition: "unreviewed" | "ignored") => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (nextDisposition === "ignored") {
        next.set("source_view", "ignored");
      } else {
        next.delete("source_view");
      }
      return next;
    });
  };
  const sources = useInfiniteQuery({
    queryKey: ["sources", disposition],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ disposition, limit: "50" });
      if (pageParam) {
        params.set("cursor", pageParam);
      }
      return apiJSON<SourceListResponse>(`/api/sources?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
  const albums = sources.data?.pages.flatMap((page) => page.albums);
  const discover = useMutation({
    mutationFn: () =>
      apiJSON<DiscoveryResponse>("/api/sources/discover", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["sources"] });
    },
  });
  return (
    <section aria-labelledby="sources-title" className="source-workspace">
      <header className="source-header">
        <div>
          <p className="step-label">Curator workspace</p>
          <h2 id="sources-title">Source albums</h2>
          <p>
            Inspect owned Immich albums privately. Nothing here is visible to
            Recipients.
          </p>
        </div>
        <div className="source-header-actions">
          <button
            className="source-organize"
            onClick={() => {
              setSearchParams((current) => {
                const next = new URLSearchParams(current);
                next.set("workspace", "drafts");
                return next;
              });
            }}
            type="button"
          >
            Organize drafts
          </button>
          <button
            className="source-connect"
            disabled={discover.isPending}
            onClick={() => discover.mutate()}
            type="button"
          >
            {discover.isPending ? "Validating…" : "Connect and discover"}
          </button>
          <button
            className="source-sign-out"
            disabled={signOutPending}
            onClick={onSignOut}
            type="button"
          >
            {signOutPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </header>
      {discover.data ? (
        <p aria-live="polite" className="source-success">
          Immich v3.0.3 connected. Found {discover.data.discovered_count} owned
          {discover.data.discovered_count === 1 ? " album" : " albums"}.
        </p>
      ) : null}
      <ErrorMessage error={discover.error} />
      <ErrorMessage error={signOutError} />
      <div aria-label="Source album views" className="source-tabs" role="group">
        <button
          aria-pressed={disposition === "unreviewed"}
          onClick={() => selectDisposition("unreviewed")}
          type="button"
        >
          Inbox
        </button>
        <button
          aria-pressed={disposition === "ignored"}
          onClick={() => selectDisposition("ignored")}
          type="button"
        >
          Ignored
        </button>
      </div>
      <p aria-live="polite" className="visually-hidden" role="status">
        {triageStatus}
      </p>
      {sources.isPending ? (
        <p className="source-empty">Loading Source albums…</p>
      ) : null}
      {sources.isError ? <ErrorMessage error={sources.error} /> : null}
      {albums?.length === 0 ? (
        <p className="source-empty">
          {disposition === "ignored"
            ? "No ignored Source albums."
            : "No unreviewed Source albums. Connect Immich to discover owned albums."}
        </p>
      ) : null}
      <div className="source-list">
        {albums?.map((album) => (
          <SourceAlbumCard
            album={album}
            csrfToken={session.csrf_token}
            key={album.id}
            onTriaged={setTriageStatus}
          />
        ))}
      </div>
      {sources.hasNextPage ? (
        <button
          className="source-load-more"
          disabled={sources.isFetchingNextPage}
          onClick={() => void sources.fetchNextPage()}
          type="button"
        >
          {sources.isFetchingNextPage ? "Loading…" : "Load more Source albums"}
        </button>
      ) : null}
    </section>
  );
}

function RecipientOnboarding({
  session,
  onComplete,
  onSignOut,
}: {
  session: SessionResponse;
  onComplete: (session: SessionResponse) => void;
  onSignOut: () => void;
}) {
  const [editedDraft, setDraft] = useState<OnboardingRequest>();
  const progress = useQuery({
    queryKey: ["onboarding"],
    queryFn: () => apiJSON<OnboardingResponse>("/api/onboarding"),
    retry: false,
  });
  const draft: OnboardingRequest = editedDraft ?? {
    privacy_acknowledged: progress.data?.privacy_acknowledged ?? false,
    engagement_acknowledged: progress.data?.engagement_acknowledged ?? false,
    interest_list_acknowledged:
      progress.data?.interest_list_acknowledged ?? false,
    email_previews_acknowledged:
      progress.data?.email_previews_acknowledged ?? false,
    push_guidance_acknowledged:
      progress.data?.push_guidance_acknowledged ?? false,
    email_preference: progress.data?.email_preference ?? "immediate",
    session_type: progress.data?.session_type ?? "",
  };
  const save = useMutation({
    mutationFn: () =>
      apiJSON<OnboardingResponse>("/api/onboarding", {
        method: "PATCH",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(draft),
      }),
  });
  const complete = useMutation({
    mutationFn: async () => {
      const response = await apiJSON<OnboardingCompleteResponse>(
        "/api/onboarding/complete",
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify(draft),
        },
      );
      return {
        ...session,
        session_type: draft.session_type,
        csrf_token: response.csrf_token,
        onboarding_required: false,
      };
    },
    onSuccess: onComplete,
  });
  const busy = save.isPending || complete.isPending;
  const updateDraft = (next: OnboardingRequest) => {
    save.reset();
    setDraft(next);
  };
  const pushSupported =
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window;

  if (progress.isPending) {
    return <p aria-live="polite">Restoring your Onboarding choices…</p>;
  }
  if (progress.isError) {
    return <ErrorMessage error={progress.error} />;
  }
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
            complete.mutate();
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
              <strong>Private individual access</strong>
              Your email and Session are personal. There are no public galleries
              or reusable shared Media links.
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
              <strong>Curator-visible engagement</strong>
              Meaningful signed-in activity is visible to the Curator. Email
              opens, prefetching, and incidental thumbnails are excluded.
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
              <strong>Interest choices suggest, never authorize</strong>
              Your explicit choices below help the Curator prepare Audience
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
              <strong>Private email previews</strong>
              Immediate messages can include one authorized cover and weekly
              digests up to three. Each embedded preview is a permanent
              low-resolution mailbox copy that can be forwarded and cannot be
              recalled after Withdrawal or Revocation. Messages contain no
              tracking pixels, public Media links, hidden counts, or hidden
              Moments.
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
          {save.isSuccess ? (
            <p aria-live="polite">Your Onboarding choices were saved.</p>
          ) : null}
          <div className="setup-secondary-actions">
            <button disabled={busy} onClick={() => save.mutate()} type="button">
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

function PublicSessionBanner({ session }: { session: SessionResponse }) {
  if (session.session_type !== "public") return null;
  return (
    <aside className="public-session-warning">
      <strong>Public computer</strong>
      <span>
        Sign out when finished. Push is disabled, and downloaded originals or
        archives remain on this computer after sign-out.
      </span>
    </aside>
  );
}

function SessionManager({
  session,
  onSignedOut,
}: {
  session: SessionResponse;
  onSignedOut: () => void;
}) {
  const queryClient = useQueryClient();
  const [labels, setLabels] = useState<Record<string, string>>({});
  const [newEmail, setNewEmail] = useState("");
  const [emailChange, setEmailChange] = useState<EmailChangeStartResponse>();
  const [oldCode, setOldCode] = useState("");
  const [newCode, setNewCode] = useState("");
  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => apiJSON<SessionListResponse>("/api/sessions"),
    retry: false,
  });
  const rename = useMutation({
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      apiNoContent(`/api/sessions/${id}`, {
        method: "PATCH",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ label }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      apiNoContent(`/api/sessions/${id}`, {
        method: "DELETE",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: async (_, id) => {
      if (sessions.data?.sessions.find((item) => item.id === id)?.current) {
        onSignedOut();
      } else {
        await queryClient.invalidateQueries({ queryKey: ["sessions"] });
      }
    },
  });
  const signOutAll = useMutation({
    mutationFn: () =>
      apiNoContent("/api/sessions/sign-out-all", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: onSignedOut,
  });
  const startEmailChange = useMutation({
    mutationFn: () =>
      apiJSON<EmailChangeStartResponse>("/api/me/email-change/request", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ new_email: newEmail }),
      }),
    onSuccess: setEmailChange,
  });
  const completeEmailChange = useMutation({
    mutationFn: (request: EmailChangeCompleteRequest) =>
      apiJSON<EmailChangeResponse>("/api/me/email-change/complete", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: () => window.location.reload(),
  });
  return (
    <details className="session-manager">
      <summary>Sessions and login email</summary>
      <p>
        Inspect and revoke each browser separately. Approximate location appears
        only when the operator configured a local GeoIP database.
      </p>
      {sessions.data?.sessions.map((item) => (
        <article
          aria-label={`Session ${item.label || `${item.browser} on ${item.platform}`}`}
          className="session-row"
          key={item.id}
        >
          <div>
            <strong>
              {item.label || `${item.browser} on ${item.platform}`}
              {item.current ? " (this browser)" : ""}
            </strong>
            <span>
              {item.session_type === "public"
                ? "Public computer"
                : "Trusted device"}
              {` · ${item.status} · created ${formatSourceDate(item.created_at)} · last active ${formatSourceDate(item.last_activity_at)}`}
              {item.location ? ` · ${item.location}` : ""}
            </span>
            {!item.push_allowed ? <span>Push unavailable</span> : null}
          </div>
          <label>
            Session name
            <input
              aria-label={`Session name for ${item.label || `${item.browser} on ${item.platform}`}`}
              onChange={(event) =>
                setLabels((current) => ({
                  ...current,
                  [item.id]: event.target.value,
                }))
              }
              value={labels[item.id] ?? item.label}
            />
          </label>
          <button
            aria-label={`Save name for ${item.label || `${item.browser} on ${item.platform}`}`}
            disabled={rename.isPending || item.status !== "active"}
            onClick={() =>
              rename.mutate({
                id: item.id,
                label: labels[item.id] ?? item.label,
              })
            }
            type="button"
          >
            Save name
          </button>
          <button
            aria-label={`${item.current ? "Sign out" : "Revoke"} ${item.label || `${item.browser} on ${item.platform}`}`}
            className="danger-button"
            disabled={revoke.isPending || item.status !== "active"}
            onClick={() => revoke.mutate(item.id)}
            type="button"
          >
            {item.current ? "Sign out this browser" : "Revoke Session"}
          </button>
        </article>
      ))}
      <ErrorMessage
        error={
          sessions.error ?? rename.error ?? revoke.error ?? signOutAll.error
        }
      />
      <button
        className="danger-button"
        disabled={signOutAll.isPending}
        onClick={() => signOutAll.mutate()}
        type="button"
      >
        Sign out all Sessions
      </button>
      <div className="email-change">
        <h3>Change login email</h3>
        {!emailChange ? (
          <>
            <label>
              New login email
              <input
                onChange={(event) => setNewEmail(event.target.value)}
                type="email"
                value={newEmail}
              />
            </label>
            <button
              disabled={startEmailChange.isPending || !newEmail}
              onClick={() => startEmailChange.mutate()}
              type="button"
            >
              Send codes to both addresses
            </button>
          </>
        ) : (
          <>
            <p>Enter the fresh code sent to each address.</p>
            <label>
              Current-address code
              <input
                inputMode="numeric"
                onChange={(event) => setOldCode(event.target.value)}
                value={oldCode}
              />
            </label>
            <label>
              New-address code
              <input
                inputMode="numeric"
                onChange={(event) => setNewCode(event.target.value)}
                value={newCode}
              />
            </label>
            <button
              disabled={completeEmailChange.isPending}
              onClick={() =>
                completeEmailChange.mutate({
                  request_id: emailChange.request_id,
                  old_code: oldCode,
                  new_code: newCode,
                })
              }
              type="button"
            >
              Confirm email change
            </button>
          </>
        )}
        <ErrorMessage
          error={startEmailChange.error ?? completeEmailChange.error}
        />
      </div>
    </details>
  );
}

function ReadyCard({
  session,
  onComplete,
  onSignOut,
}: {
  session?: SessionResponse;
  onComplete: (session: SessionResponse) => void;
  onSignOut: () => void;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [draftsDirty, setDraftsDirty] = useState(false);
  const [draftsSaving, setDraftsSaving] = useState(false);
  const draftsRequested = searchParams.get("workspace") === "drafts";
  const signOut = useMutation({
    mutationFn: () => {
      if (!session) throw new Error("A Session is required to sign out.");
      return apiNoContent("/api/session/logout", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      });
    },
    onSuccess: onSignOut,
  });

  useEffect(() => {
    const restoreDraftLocation = () =>
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          next.set("workspace", "drafts");
          return next;
        },
        { replace: true },
      );
    const protectDraftHistory = () => {
      if (!draftsDirty) return;
      if (draftsSaving) {
        restoreDraftLocation();
        return;
      }
      if (window.confirm("Discard changes that have not finished saving?"))
        setDraftsDirty(false);
      else restoreDraftLocation();
    };
    window.addEventListener("popstate", protectDraftHistory);
    return () => window.removeEventListener("popstate", protectDraftHistory);
  }, [draftsDirty, draftsSaving, setSearchParams]);

  if (session?.curator && (draftsRequested || draftsDirty)) {
    return (
      <section className="draft-work-shell">
        <PublicSessionBanner session={session} />
        <SessionManager onSignedOut={onSignOut} session={session} />
        <div className="draft-work-actions">
          <button
            className="back-to-management"
            onClick={() => {
              if (
                draftsDirty &&
                !window.confirm(
                  "Discard changes that have not finished saving?",
                )
              )
                return;
              setDraftsDirty(false);
              setSearchParams((current) => {
                const next = new URLSearchParams(current);
                next.delete("workspace");
                return next;
              });
            }}
            disabled={draftsSaving}
            type="button"
          >
            Back to Curator management
          </button>
          <button
            className="source-sign-out"
            disabled={signOut.isPending}
            onClick={() => {
              if (
                draftsDirty &&
                !window.confirm(
                  "Discard changes that have not finished saving and sign out?",
                )
              )
                return;
              signOut.mutate();
            }}
            type="button"
          >
            {signOut.isPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
        <ErrorMessage error={signOut.error} />
        <EventOrganizer
          onDirtyChange={setDraftsDirty}
          onSavingChange={setDraftsSaving}
          session={session}
        />
      </section>
    );
  }
  if (session) {
    if (session.onboarding_required) {
      return (
        <RecipientOnboarding
          onComplete={onComplete}
          onSignOut={onSignOut}
          session={session}
        />
      );
    }
    if (!session.curator) {
      return (
        <>
          <InvitationSuggestions session={session} />
          <PublicSessionBanner session={session} />
          <SessionManager onSignedOut={onSignOut} session={session} />
          <RecipientVisibilityManager onSignOut={onSignOut} session={session} />
        </>
      );
    }
    return (
      <>
        <PublicSessionBanner session={session} />
        <SessionManager onSignedOut={onSignOut} session={session} />
        <PeopleManager session={session} />
        <InvitationSuggestions session={session} />
        <FamilyManager session={session} />
        <VisibilityManager session={session} />
        <section className="shell-card curator-card">
          <SourceWorkspace
            onSignOut={() => signOut.mutate()}
            session={session}
            signOutError={signOut.error}
            signOutPending={signOut.isPending}
          />
        </section>
        <section className="shell-card curator-card">
          <RepairWorkspace csrfToken={session.csrf_token} />
        </section>
      </>
    );
  }
  return <SignInFlow onComplete={onComplete} />;
}

function InvitationLanding() {
  const [searchParams] = useSearchParams();
  const [token] = useState(() => searchParams.get("token") ?? "");
  const [accepted, setAccepted] = useState(false);
  const [acceptedSession, setAcceptedSession] = useState<SessionResponse>();
  const [exitedInvitation, setExitedInvitation] = useState(false);
  const invitation = useQuery({
    queryKey: ["invitation", token],
    queryFn: () =>
      apiJSON<InspectResponse>("/api/auth/invitations/inspect", {
        headers: { "X-Memento-Invitation": token },
      }),
    enabled: token.length > 0,
    retry: false,
  });
  const accept = useMutation({
    mutationFn: () =>
      apiJSON<AcceptResponse>("/api/auth/invitations/accept", {
        method: "POST",
        body: JSON.stringify({ token }),
      }),
    onSuccess: () => {
      window.history.replaceState({}, "", "/");
      setAccepted(true);
    },
  });
  const acceptedIdentity = useQuery({
    queryKey: ["accepted-invitation-session"],
    queryFn: () => apiJSON<SessionResponse>("/api/session"),
    enabled: accepted,
    retry: 2,
    retryDelay: 0,
  });
  const currentSession = accepted
    ? (acceptedSession ?? acceptedIdentity.data)
    : undefined;

  if (exitedInvitation) {
    return <MementoApp />;
  }

  if (currentSession) {
    return (
      <main>
        <ReadyCard
          onComplete={setAcceptedSession}
          onSignOut={() => {
            setAccepted(false);
            setAcceptedSession(undefined);
            setExitedInvitation(true);
          }}
          session={currentSession}
        />
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
        {!accepted && (!token || invitation.isError) ? (
          <p className="form-error" role="alert">
            This Invitation is invalid or no longer available.
          </p>
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

function MementoApp() {
  const queryClient = useQueryClient();
  const [completedSession, setCompletedSession] = useState<SessionResponse>();
  const [signedOut, setSignedOut] = useState(false);
  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: fetchBootstrap,
    retry: false,
  });

  const completeSession = (session: SessionResponse) => {
    setSignedOut(false);
    setCompletedSession(session);
  };

  const signOut = () => {
    queryClient.removeQueries({
      predicate: (query) => query.queryKey[0] !== "bootstrap",
    });
    setCompletedSession(undefined);
    setSignedOut(true);
  };

  if (completedSession && !signedOut) {
    return (
      <main>
        <ReadyCard
          onComplete={completeSession}
          onSignOut={signOut}
          session={completedSession}
        />
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
        <SetupFlow onComplete={completeSession} />
      </main>
    );
  }

  const session =
    !signedOut && bootstrap.data.kind === "session"
      ? bootstrap.data.session
      : undefined;
  return (
    <main>
      <ReadyCard
        onComplete={completeSession}
        onSignOut={signOut}
        session={session}
      />
    </main>
  );
}

export function App() {
  return window.location.pathname === "/invitation" ? (
    <InvitationLanding />
  ) : (
    <MementoApp />
  );
}
