import { Navigate, Outlet, useLocation } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { errorMessage } from "../../lib/http";
import { ConnectionStatus } from "../connection/connection-status";
import { SignInForm } from "../identity/sign-in-form";
import { Header } from "../shell/header";
import { Button } from "../ui/button";

const pageClassName =
  "mx-auto max-w-[1440px] px-5 pt-10 pb-14 min-[381px]:px-6 min-[761px]:px-12 min-[761px]:pt-17 min-[761px]:pb-20";
const headingClassName =
  "font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] font-normal tracking-[-1px] text-balance";

function destination(person: { is_curator: boolean } | null | undefined) {
  return person
    ? person.is_curator
      ? "/curator"
      : "/access-denied"
    : "/sign-in";
}

export function AppShell() {
  return (
    <>
      <Header />
      <Outlet />
    </>
  );
}

export function InstallationLayout() {
  const status = useIdentityStatus();
  const location = useLocation();
  if (status.isPending)
    return (
      <main className={pageClassName}>
        <p className="mb-5 text-muted" role="status">
          Loading memento…
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
    return <Navigate replace to="/setup" />;
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
    return <Navigate replace to={destination(data?.person)} />;
  return (
    <main className={pageClassName}>
      <Outlet />
    </main>
  );
}

export function SetupPage() {
  return (
    <>
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
      <div className="mb-9 max-w-160 min-[761px]:mb-12">
        <h1 className={headingClassName}>Welcome back</h1>
        <p className="mt-5 max-w-[590px] text-muted">
          Sign in with the identity you used to set up memento.
        </p>
      </div>
      <SignInForm />
    </>
  );
}

export function CuratorLayout() {
  const { data } = useIdentityStatus();
  if (!data?.person?.is_curator)
    return <Navigate replace to={destination(data?.person)} />;
  return (
    <main className={pageClassName}>
      <Outlet />
    </main>
  );
}

export function CuratorPage() {
  return (
    <>
      <div className="mb-9.5 max-w-160">
        <h1 className={headingClassName}>Your albums</h1>
        <p className="mt-5 max-w-[590px] text-muted">
          Choose the photos and videos you want to share with friends and
          family.
        </p>
      </div>
      <section className="min-h-45 border-t border-border py-9 min-[761px]:min-h-70">
        <h2 className="font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]">
          No albums yet
        </h2>
        <p className="mt-4 max-w-105 text-muted">
          Your installation is ready. Album importing will be available in a
          future update.
        </p>
      </section>
    </>
  );
}

export function HomePage() {
  const { data } = useIdentityStatus();
  return <Navigate replace to={destination(data?.person)} />;
}

export function AccessDeniedPage() {
  return (
    <main className={pageClassName}>
      <h1 className={headingClassName}>Access denied</h1>
      <p className="mb-5 text-muted">
        This area is only available to Curators.
      </p>
    </main>
  );
}
