import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { APIError } from "./api";
import { InvitationLanding, SignInFlow } from "./Authentication";
import { CuratorActivity } from "./CuratorActivity";
import { CuratorInteractions } from "./CuratorInteractions";
import { EventOrganizer } from "./EventOrganizer";
import { FamilyManager } from "./FamilyManager";
import { useBootstrap, type BootstrapState } from "./hooks/queries/bootstrap";
import {
  CURRENT_SESSION_QUERY_KEY,
  useCurrentSession,
  useSignOut,
} from "./hooks/queries/sessions";
import { InvitationSuggestions } from "./InvitationSuggestions";
import {
  EmailPreferences,
  PlatformEmailDefaults,
} from "./NotificationSettings";
import { PeopleManager } from "./PeopleManager";
import { BrandHeader, ErrorMessage } from "./presentation";
import { OfflineNotice, PWAUpdatePrompt, ThemeToggle } from "./PWAControls";
import { PWA_RESTART_GUARD_EVENT, type PWARestartGuardDetail } from "./pwa";
import { PushNotifications } from "./PushNotifications";
import { unsubscribeLocalPushBestEffort } from "./push";
import {
  PublicSessionBanner,
  RecipientOnboarding,
  RecipientOnboardingReview,
} from "./RecipientOnboarding";
import { RecipientLibrary } from "./RecipientLibrary";
import { RecoveryCard } from "./Recovery";
import { RepairWorkspace } from "./RepairWorkspace";
import { SessionManager } from "./SessionManagement";
import { SetupFlow } from "./Setup";
import { SourceWorkspace } from "./SourceInbox";
import type { SessionResponse } from "./types/generated/setup";
import { useOnlineStatus } from "./useOnlineStatus";
import {
  RecipientVisibilityManager,
  VisibilityManager,
} from "./VisibilityManager";

function removeProtectedQueries(
  queryClient: QueryClient,
  keepRecoveryReview = false,
  keepCurrentSession = false,
  keepSessionConfirmation = false,
) {
  if (!keepCurrentSession) {
    void queryClient.resetQueries({
      queryKey: CURRENT_SESSION_QUERY_KEY,
      exact: true,
    });
  }
  queryClient.removeQueries({
    predicate: (query) =>
      query.queryKey[0] !== "bootstrap" &&
      query.queryKey[0] !== "current-session" &&
      (!keepRecoveryReview || query.queryKey[0] !== "recovery-review") &&
      (!keepSessionConfirmation || query.queryKey[0] !== "sign-in-session"),
  });
}

function ReadyCard({
  session,
  onComplete,
  onSignOut,
  online,
}: {
  session?: SessionResponse;
  onComplete: (session: SessionResponse) => void;
  onSignOut: () => void;
  online: boolean;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [draftsDirty, setDraftsDirty] = useState(false);
  const [draftsSaving, setDraftsSaving] = useState(false);
  const [preserveDraftOffline, setPreserveDraftOffline] = useState(false);
  const [recipientAccountIdentity, setRecipientAccountIdentity] = useState("");
  const recipientAccountOpen =
    recipientAccountIdentity !== "" &&
    recipientAccountIdentity === session?.csrf_token;
  const draftsRequested = searchParams.get("workspace") === "drafts";
  const signOut = useSignOut(session?.csrf_token, onSignOut);

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
      if (!draftsDirty && !draftsSaving) return;
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

  useEffect(() => {
    const protectDraftOnOffline = () => {
      if (!draftsDirty && !draftsSaving) {
        setPreserveDraftOffline(false);
        return;
      }
      if (
        draftsSaving ||
        !window.confirm(
          "Discard changes that have not finished saving and go offline?",
        )
      ) {
        setPreserveDraftOffline(true);
        return;
      }
      setDraftsDirty(false);
      setPreserveDraftOffline(false);
    };
    const resetOfflineProtection = () => setPreserveDraftOffline(false);
    const protectDraftOnRestart = (event: Event) => {
      if (!draftsDirty && !draftsSaving) return;
      const restartEvent = event as CustomEvent<PWARestartGuardDetail>;
      if (draftsSaving) {
        restartEvent.detail.blockedBy = "saving";
        event.preventDefault();
        return;
      }
      if (
        !window.confirm(
          "Discard changes that have not finished saving and update Memento?",
        )
      ) {
        restartEvent.detail.blockedBy = "dirty";
        event.preventDefault();
        return;
      }
      setDraftsDirty(false);
    };
    window.addEventListener("offline", protectDraftOnOffline);
    window.addEventListener("online", resetOfflineProtection);
    window.addEventListener(PWA_RESTART_GUARD_EVENT, protectDraftOnRestart);
    return () => {
      window.removeEventListener("offline", protectDraftOnOffline);
      window.removeEventListener("online", resetOfflineProtection);
      window.removeEventListener(
        PWA_RESTART_GUARD_EVENT,
        protectDraftOnRestart,
      );
    };
  }, [draftsDirty, draftsSaving]);

  const keepDraftOpenOffline =
    session?.curator &&
    !online &&
    (draftsDirty || draftsSaving) &&
    preserveDraftOffline;
  if (session && !online && !keepDraftOpenOffline) {
    return (
      <div className="offline-shell">
        <ThemeToggle />
        <OfflineNotice />
      </div>
    );
  }

  if (session?.curator && (draftsRequested || draftsDirty || draftsSaving)) {
    return (
      <section className="draft-work-shell">
        <PublicSessionBanner
          disabled={draftsSaving}
          onSignOut={() => signOut.mutate()}
          session={session}
        />
        <SessionManager
          mutationsDisabled={draftsSaving}
          onSignedOut={onSignOut}
          session={session}
        />
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
            disabled={signOut.isPending || draftsSaving}
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
        {!online ? (
          <p className="form-error" role="alert">
            Memento is offline. Reconnect before leaving while these changes
            remain unsaved.
          </p>
        ) : null}
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
          <PublicSessionBanner
            onSignOut={() => signOut.mutate()}
            session={session}
          />
          <div className="recipient-experience">
            <RecipientLibrary session={session} />
            <details
              className="recipient-account-tools"
              onToggle={(event) =>
                setRecipientAccountIdentity(
                  event.currentTarget.open ? session.csrf_token : "",
                )
              }
              open={recipientAccountOpen}
            >
              <summary aria-label={`Account for ${session.display_name}`}>
                <span aria-hidden="true">
                  {session.display_name.trim().charAt(0).toUpperCase() || "M"}
                </span>
                <strong>Account</strong>
              </summary>
              <div className="recipient-account-drawer">
                <RecipientOnboardingReview />
                <EmailPreferences session={session} />
                <PushNotifications session={session} />
                <InvitationSuggestions session={session} />
                <SessionManager onSignedOut={onSignOut} session={session} />
                {recipientAccountOpen ? (
                  <RecipientVisibilityManager
                    onSignOut={onSignOut}
                    session={session}
                  />
                ) : null}
              </div>
            </details>
          </div>
        </>
      );
    }
    return (
      <>
        <PublicSessionBanner
          onSignOut={() => signOut.mutate()}
          session={session}
        />
        <CuratorActivity session={session} />
        <SessionManager onSignedOut={onSignOut} session={session} />
        <EmailPreferences session={session} />
        <PushNotifications session={session} />
        <PlatformEmailDefaults session={session} />
        <div id="curator-people">
          <PeopleManager session={session} />
        </div>
        <InvitationSuggestions session={session} />
        <FamilyManager session={session} />
        <VisibilityManager session={session} />
        <CuratorInteractions session={session} />
        <section className="shell-card curator-card" id="curator-sources">
          <SourceWorkspace
            onSignOut={() => signOut.mutate()}
            session={session}
            signOutError={signOut.error}
            signOutPending={signOut.isPending}
          />
        </section>
        <section className="shell-card curator-card" id="curator-repairs">
          <RepairWorkspace csrfToken={session.csrf_token} />
        </section>
      </>
    );
  }
  return <SignInFlow onComplete={onComplete} />;
}

function MementoApp({
  initialSession,
}: { initialSession?: SessionResponse } = {}) {
  const queryClient = useQueryClient();
  const online = useOnlineStatus();
  const [signedOut, setSignedOut] = useState(false);
  const [confirmingRecoveryRelease, setConfirmingRecoveryRelease] =
    useState(false);
  const bootstrap = useBootstrap(!initialSession || signedOut);
  const currentSession = useCurrentSession();
  const bootstrapSession =
    bootstrap.data?.kind === "session" || bootstrap.data?.kind === "recovery"
      ? bootstrap.data.session
      : undefined;
  const session = !signedOut
    ? (currentSession.data ?? initialSession ?? bootstrapSession)
    : undefined;
  const clearProtectedData = useCallback(
    (
      keepRecoveryReview = false,
      keepCurrentSession = false,
      keepSessionConfirmation = false,
    ) => {
      removeProtectedQueries(
        queryClient,
        keepRecoveryReview,
        keepCurrentSession,
        keepSessionConfirmation,
      );
      queryClient.getMutationCache().clear();
    },
    [queryClient],
  );
  const revokeLocalSession = useCallback(() => {
    void unsubscribeLocalPushBestEffort();
    clearProtectedData();
    setSignedOut(true);
  }, [clearProtectedData]);

  useEffect(() => {
    if (!signedOut && online && session) {
      queryClient.setQueryData(CURRENT_SESSION_QUERY_KEY, session);
    }
  }, [online, queryClient, session, signedOut]);

  useEffect(() => {
    if (currentSession.data) {
      queryClient.removeQueries({ queryKey: ["sign-in-session"] });
    }
  }, [currentSession.data, queryClient]);

  useEffect(() => {
    if (!online || bootstrap.data?.kind === "closed") {
      clearProtectedData();
      return;
    }
    if (bootstrap.data?.kind === "recovery") {
      clearProtectedData(true, Boolean(bootstrap.data.session));
    }
  }, [bootstrap.data, clearProtectedData, online]);

  useEffect(
    () =>
      queryClient.getQueryCache().subscribe((event) => {
        if (event.type !== "updated") return;
        const queryKey = event.query.queryKey as readonly unknown[];
        if (
          queryKey[0] === "bootstrap" ||
          !(event.query.state.error instanceof APIError) ||
          event.query.state.error.status !== 401
        )
          return;
        revokeLocalSession();
      }),
    [queryClient, revokeLocalSession],
  );
  useEffect(
    () =>
      queryClient.getMutationCache().subscribe((event) => {
        if (
          event.type !== "updated" ||
          !(event.mutation.state.error instanceof APIError) ||
          event.mutation.state.error.status !== 401
        )
          return;
        revokeLocalSession();
      }),
    [queryClient, revokeLocalSession],
  );

  const completeSession = (nextSession: SessionResponse) => {
    void queryClient.cancelQueries({ queryKey: ["bootstrap"], exact: true });
    const currentBootstrap = queryClient.getQueryData<BootstrapState>([
      "bootstrap",
    ]);
    const nextBootstrap: BootstrapState =
      currentBootstrap?.kind === "recovery"
        ? { kind: "recovery", session: nextSession }
        : { kind: "session", session: nextSession };
    queryClient.setQueryData(["bootstrap"], nextBootstrap);
    clearProtectedData(false, true, true);
    queryClient.setQueryData(CURRENT_SESSION_QUERY_KEY, nextSession);
    setSignedOut(false);
  };
  const signOut = () => {
    void unsubscribeLocalPushBestEffort();
    clearProtectedData();
    setSignedOut(true);
  };
  const confirmRecoveryRelease = () => {
    setConfirmingRecoveryRelease(true);
    void bootstrap.refetch().then((result) => {
      if (!result.isSuccess) return;
      if (result.data.kind === "recovery") {
        setConfirmingRecoveryRelease(false);
        return;
      }
      const nextSession =
        result.data.kind === "session" ? result.data.session : undefined;
      clearProtectedData(false, Boolean(nextSession));
      if (nextSession) {
        queryClient.setQueryData(CURRENT_SESSION_QUERY_KEY, nextSession);
      }
      setConfirmingRecoveryRelease(false);
    });
  };

  if (bootstrap.data?.kind === "recovery") {
    return (
      <main>
        <RecoveryCard
          confirmationError={bootstrap.isError ? bootstrap.error : null}
          confirmationPending={bootstrap.isFetching}
          confirmingRelease={confirmingRecoveryRelease}
          onComplete={completeSession}
          onReleased={confirmRecoveryRelease}
          onRetryConfirmation={confirmRecoveryRelease}
          session={session}
        />
      </main>
    );
  }
  if (session && currentSession.data) {
    return (
      <main>
        <ReadyCard
          onComplete={completeSession}
          onSignOut={signOut}
          online={online}
          session={session}
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
        {online ? (
          <section aria-labelledby="memento-title" className="shell-card">
            <BrandHeader />
            <p className="lede" role="alert">
              Memento could not verify access to your authorized library. It has
              not been reported as empty.
            </p>
            <button onClick={() => void bootstrap.refetch()} type="button">
              Try again
            </button>
          </section>
        ) : (
          <OfflineNotice />
        )}
      </main>
    );
  }
  if (bootstrap.data.kind === "available")
    return (
      <main>
        <SetupFlow onComplete={completeSession} />
      </main>
    );
  return (
    <main>
      <ReadyCard
        onComplete={completeSession}
        onSignOut={signOut}
        online={online}
        session={session}
      />
    </main>
  );
}

function ApplicationRoute() {
  const queryClient = useQueryClient();
  const clearInvitationIdentity = useCallback(() => {
    removeProtectedQueries(queryClient);
    queryClient.getMutationCache().clear();
  }, [queryClient]);
  if (window.location.pathname === "/invitation") {
    return (
      <InvitationLanding
        onIdentityTransition={clearInvitationIdentity}
        renderApplication={(session) => <MementoApp initialSession={session} />}
      />
    );
  }
  return <MementoApp />;
}

export function App() {
  return (
    <>
      <PWAUpdatePrompt />
      <ApplicationRoute />
    </>
  );
}
