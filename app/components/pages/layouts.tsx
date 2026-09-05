import { Navigate, Outlet, useLocation } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { errorMessage } from "../../lib/http";
import { ConnectionStatus } from "../connection/connection-status";
import { SignInForm } from "../identity/sign-in-form";
import { Header } from "../shell/header";
import { Button } from "../ui/button";

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
      <main className="state-main">
        <p role="status">Loading memento…</p>
      </main>
    );
  if (status.isError && !status.data)
    return (
      <main className="state-main">
        <p role="alert">{errorMessage(status.error)}</p>
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
    <main className="public-main">
      <Outlet />
    </main>
  );
}

export function SetupPage() {
  return (
    <>
      <div className="page-intro">
        <h1>Make room for your memories</h1>
        <p>
          The first successful sign-in claims this installation and makes you
          its first Curator. You'll choose what to share from Immich and who can
          see it.
        </p>
      </div>
      <div className="setup-content">
        <SignInForm claiming />
        <ConnectionStatus area="setup" />
      </div>
    </>
  );
}

export function SignInPage() {
  return (
    <>
      <div className="page-intro">
        <h1>Welcome back</h1>
        <p>Sign in with the identity you used to set up memento.</p>
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
    <main className="curator-main">
      <Outlet />
    </main>
  );
}

export function CuratorPage() {
  return (
    <>
      <div className="page-intro">
        <h1>Your albums</h1>
        <p>
          Choose the photos and videos you want to share with friends and
          family.
        </p>
      </div>
      <div className="curator-content">
        <section className="empty-albums">
          <h2>No albums yet</h2>
          <p>
            Your installation is ready. Album importing will be available in a
            future update.
          </p>
        </section>
        <ConnectionStatus area="curator" />
      </div>
    </>
  );
}

export function HomePage() {
  const { data } = useIdentityStatus();
  return <Navigate replace to={destination(data?.person)} />;
}

export function AccessDeniedPage() {
  return (
    <main className="state-main">
      <h1>Access denied</h1>
      <p>This area is only available to Curators.</p>
    </main>
  );
}
