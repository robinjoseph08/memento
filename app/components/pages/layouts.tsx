import { useEffect, useMemo, useRef, useState } from "react";
import { Navigate, Outlet, useLocation } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { useUnsavedChangesBlocker } from "../../hooks/use-unsaved-changes";
import { UnsavedChangesContext } from "../../lib/forms";
import { errorMessage } from "../../lib/http";
import { ConnectionStatus } from "../connection/connection-status";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { SignInForm } from "../identity/sign-in-form";
import { Header } from "../shell/header";
import { PageTitle } from "../shell/page-title";
import { PreviewModeContext } from "../shell/preview-mode";
import { Button } from "../ui/button";

export const pageClassName =
  "mx-auto max-w-[1440px] px-5 pt-10 pb-14 min-[381px]:px-6 min-[761px]:px-12 min-[761px]:pt-17 min-[761px]:pb-20";
const headingClassName =
  "font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] font-normal tracking-[-1px] text-balance";

// The Mobile App opens sign-in with a return link. It rides along as a query
// parameter through sign-in and Onboarding, and once the Person is signed in
// and onboarded, the server sends the browser sheet back to the app with it.
const mobileReturnPath = "/api/identity/mobile/return";

type Visitor =
  { is_curator: boolean; onboarding_completed_at?: string } | null | undefined;

function destination(person: Visitor, search: string) {
  const returnTo = new URLSearchParams(search).get("return_to") ?? "";
  const carried = returnTo ? `?return_to=${encodeURIComponent(returnTo)}` : "";
  if (!person) return `/sign-in${carried}`;
  if (!person.onboarding_completed_at) return `/welcome${carried}`;
  if (returnTo) return `${mobileReturnPath}${carried}`;
  return person.is_curator ? "/curator" : "/albums";
}

// Redirect sends the Person where they belong. The Mobile App's return is
// served by the server, so it is a full navigation rather than a route change.
function Redirect({ person }: { person: Visitor }) {
  const { search } = useLocation();
  const to = destination(person, search);
  const external = to.startsWith(mobileReturnPath);
  useEffect(() => {
    if (external) window.location.replace(to);
  }, [external, to]);
  if (!external) return <Navigate replace to={to} />;
  return (
    <main className={pageClassName}>
      <PageTitle title="Opening the app" />
      <p className="mb-5 text-muted" role="status">
        Opening the Memento app…
      </p>
      <Button asChild variant="outline">
        <a href={to}>Open the Memento app</a>
      </Button>
    </main>
  );
}

// Every signed-in area shares this guard, so a Person with unfinished
// Onboarding resumes it from any bookmark, old session, or later sign-in.
function onboardingPending(
  person: { onboarding_completed_at?: string } | null | undefined,
) {
  return !!person && !person.onboarding_completed_at;
}

export function AppShell() {
  const { data } = useIdentityStatus();
  const unsavedRef = useRef(new Map<symbol, boolean>());
  const blocker = useUnsavedChangesBlocker(unsavedRef);
  const [previewActive, setPreviewActive] = useState(false);
  const previewMode = useMemo(
    () => ({ active: previewActive, setActive: setPreviewActive }),
    [previewActive],
  );
  return (
    <UnsavedChangesContext value={unsavedRef}>
      <PreviewModeContext value={previewMode}>
        <Header />
        <Outlet
          key={`${data?.person?.id ?? "public"}-${!!data?.person?.is_curator}`}
        />
        <ConfirmDialog
          confirmLabel="Leave page"
          description="Your changes will not be saved."
          onConfirm={() => blocker.proceed?.()}
          onOpenChange={(open) => {
            if (!open) blocker.reset?.();
          }}
          open={blocker.state === "blocked"}
          title="Leave this page?"
        />
      </PreviewModeContext>
    </UnsavedChangesContext>
  );
}

export function InstallationLayout() {
  const status = useIdentityStatus();
  const location = useLocation();
  if (status.isPending)
    return (
      <main className={pageClassName}>
        <p className="mb-5 text-muted" role="status">
          Loading Memento…
        </p>
      </main>
    );
  if (status.isError && !status.data)
    return (
      <main className={pageClassName}>
        <p className="mb-5 text-muted" role="alert">
          {errorMessage(status.error)}
        </p>
        <Button
          disabled={status.isFetching}
          onClick={() => void status.refetch()}
          variant="outline"
        >
          {status.isFetching ? "Trying again…" : "Try again"}
        </Button>
      </main>
    );
  if (!status.data.claimed && location.pathname !== "/setup")
    return <Navigate replace to={`/setup${location.search}`} />;
  return (
    <>
      {status.isError && (
        <div className="mx-auto max-w-5xl px-6 pt-6">
          <p role="alert">
            Memento could not refresh your sign-in status. Try again when the
            connection is restored.
          </p>
          <Button
            disabled={status.isFetching}
            onClick={() => void status.refetch()}
            variant="outline"
          >
            {status.isFetching ? "Trying again…" : "Try again"}
          </Button>
        </div>
      )}
      <Outlet />
    </>
  );
}

export function PublicLayout() {
  const { data } = useIdentityStatus();
  const { pathname } = useLocation();
  if (data?.person || (data?.claimed && pathname === "/setup"))
    return <Redirect person={data?.person} />;
  return (
    <main className={pageClassName}>
      <Outlet />
    </main>
  );
}

export function SetupPage() {
  return (
    <>
      <PageTitle title="Setup" />
      <div className="mb-9 max-w-160 min-[761px]:mb-12">
        <h1 className={headingClassName}>Make room for your memories</h1>
        <p className="mt-5 max-w-[590px] text-muted">
          The first successful sign-in claims this installation and makes you
          its first Curator. You'll choose what to share from Immich and who can
          see it.
        </p>
      </div>
      <div className="grid max-w-115 grid-cols-1 items-start gap-10 min-[761px]:max-w-none min-[761px]:grid-cols-[minmax(0,380px)_minmax(0,370px)] min-[761px]:gap-20">
        <SignInForm claiming />
        <ConnectionStatus />
      </div>
    </>
  );
}

export function SignInPage() {
  return (
    <>
      <PageTitle title="Sign in" />
      <div className="mb-9 max-w-160 min-[761px]:mb-12">
        <h1 className={headingClassName}>Welcome back</h1>
        <p className="mt-5 max-w-[590px] text-muted">
          Sign in with an email address your Curator approved.
        </p>
      </div>
      <SignInForm />
    </>
  );
}

export function CuratorLayout({ compact = false }: { compact?: boolean }) {
  const { data } = useIdentityStatus();
  if (!data?.person?.is_curator || onboardingPending(data.person))
    return <Redirect person={data?.person} />;
  return (
    <main className={compact ? "mx-auto max-w-[1440px] pb-10" : pageClassName}>
      <Outlet />
    </main>
  );
}

export function SignedInLayout() {
  const { data } = useIdentityStatus();
  if (!data?.person || onboardingPending(data.person))
    return <Redirect person={data?.person} />;
  return (
    <main className={pageClassName}>
      <Outlet />
    </main>
  );
}

export function OnboardingLayout() {
  const { data } = useIdentityStatus();
  if (!data?.person || !onboardingPending(data.person))
    return <Redirect person={data?.person} />;
  return (
    <main className={pageClassName}>
      <Outlet />
    </main>
  );
}

export function ViewerLayout() {
  const { data } = useIdentityStatus();
  if (!data?.person || onboardingPending(data.person))
    return <Redirect person={data?.person} />;
  return (
    <main className="mx-auto max-w-[1440px] px-5 pt-6 pb-16 min-[761px]:px-12">
      <Outlet />
    </main>
  );
}

export function HomePage() {
  const { data } = useIdentityStatus();
  return (
    <>
      <PageTitle />
      <Redirect person={data?.person} />
    </>
  );
}

export function AccessDeniedPage() {
  return (
    <main className={pageClassName}>
      <PageTitle title="Access denied" />
      <h1 className={headingClassName}>Access denied</h1>
      <p className="mb-5 text-muted">
        This area is only available to Curators.
      </p>
    </main>
  );
}
