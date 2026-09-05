import { Link } from "react-router-dom";

import { useIdentityStatus, useSignOut } from "../../hooks/queries/identity";
import { errorMessage } from "../../lib/http";
import { Button } from "../ui/button";
import { ThemeToggle } from "./theme-toggle";

export function Header() {
  const { data } = useIdentityStatus();
  const signOut = useSignOut();
  return (
    <>
      <header className="app-header">
        <Link aria-label="memento home" className="wordmark" to="/">
          <svg
            aria-hidden="true"
            fill="none"
            height="36"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="3"
            viewBox="0 0 32 32"
            width="36"
          >
            <path d="M5 22V8a4 4 0 0 1 4-4h13" />
            <rect height="19" rx="3" width="19" x="11" y="10" />
          </svg>
          <span>memento</span>
        </Link>
        <div className="header-actions">
          <ThemeToggle />
          {data?.person && (
            <>
              <span className="person-name">{data.person.display_name}</span>
              {data.person.is_curator && (
                <span className="curator-label">Curator</span>
              )}
              <form
                aria-label="Sign out"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (!signOut.isPending) signOut.mutate();
                }}
              >
                <Button
                  disabled={signOut.isPending}
                  type="submit"
                  variant="ghost"
                >
                  {signOut.isPending ? "Signing out…" : "Sign out"}
                </Button>
              </form>
            </>
          )}
        </div>
      </header>
      {signOut.isError && (
        <p className="shell-error" role="alert">
          {errorMessage(signOut.error)}
        </p>
      )}
    </>
  );
}
