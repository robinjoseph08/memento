import { lazy, Suspense } from "react";
import { Link, NavLink } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { useTheme } from "../../hooks/use-theme";
import { AccountMenu } from "./account-menu";
import { MobileNavigation } from "./mobile-navigation";
import { ThemeToggle } from "./theme-toggle";
import { Wordmark } from "./wordmark";

// PROTOTYPE. The viewer prototype shows the notification bell ticket 14 adds.
const PrototypeBell =
  import.meta.env.MODE === "prototype"
    ? lazy(() =>
        import("../pages/viewer-prototype/notifications").then((module) => ({
          default: module.NotificationBell,
        })),
      )
    : null;

export function Header() {
  const { data } = useIdentityStatus();
  const theme = useTheme();
  return (
    <header className="flex min-h-16 items-center gap-2 border-b border-border px-3 py-2 min-[381px]:px-4 min-[601px]:gap-4 min-[761px]:px-8">
      {data?.person && (
        <MobileNavigation
          key={`${data.person.id}-${data.person.is_curator}`}
          person={data.person}
        />
      )}
      <Link
        aria-label="memento home"
        className="inline-flex shrink-0 cursor-pointer touch-manipulation items-center gap-1.75 [-webkit-tap-highlight-color:transparent] focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-5 focus-visible:outline-ring min-[381px]:gap-2.75"
        to="/"
      >
        <Wordmark />
      </Link>
      {data?.person && (
        <nav
          aria-label="Main navigation"
          className="hidden flex-1 gap-2 pl-8 min-[601px]:flex"
        >
          <NavLink
            className="rounded-md px-4 py-3 text-sm hover:bg-surface aria-[current=page]:bg-surface"
            end
            to={data.person.is_curator ? "/curator" : "/albums"}
          >
            Albums
          </NavLink>
          {data.person.is_curator && (
            <NavLink
              className="rounded-md px-4 py-3 text-sm hover:bg-surface aria-[current=page]:bg-surface"
              to="/curator/people"
            >
              People
            </NavLink>
          )}
        </nav>
      )}
      <div className="ml-auto flex items-center gap-2">
        {data?.person && PrototypeBell && (
          <Suspense fallback={null}>
            <PrototypeBell />
          </Suspense>
        )}
        {data?.person ? (
          <AccountMenu key={data.person.id} person={data.person} {...theme} />
        ) : (
          <ThemeToggle {...theme} />
        )}
      </div>
    </header>
  );
}
