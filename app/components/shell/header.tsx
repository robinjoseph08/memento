import { use } from "react";
import { Link, NavLink } from "react-router-dom";

import {
  pendingRequestCount,
  useAccessRequests,
} from "../../hooks/queries/admission";
import { useIdentityStatus } from "../../hooks/queries/identity";
import { useTheme } from "../../hooks/use-theme";
import { AccountMenu } from "./account-menu";
import { MobileNavigation } from "./mobile-navigation";
import { PreviewModeContext } from "./preview-mode";
import { ThemeToggle } from "./theme-toggle";
import { Wordmark } from "./wordmark";

export function Header() {
  const { data } = useIdentityStatus();
  const theme = useTheme();
  const preview = use(PreviewModeContext).active;
  const requests = useAccessRequests();
  const pending = pendingRequestCount(requests.data);
  const onboarded = !!data?.person?.onboarding_completed_at;
  return (
    <header className="flex min-h-16 items-center gap-2 border-b border-border px-3 py-2 min-[381px]:px-4 min-[601px]:gap-4 min-[761px]:px-8">
      {data?.person && onboarded && (
        <MobileNavigation
          key={`${data.person.id}-${data.person.is_curator}`}
          pendingRequests={pending}
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
      {data?.person && onboarded && (
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
            <>
              <NavLink
                className="rounded-md px-4 py-3 text-sm hover:bg-surface aria-[current=page]:bg-surface"
                to="/curator/people"
              >
                People
              </NavLink>
              <NavLink
                className="inline-flex items-center gap-2 rounded-md px-4 py-3 text-sm hover:bg-surface aria-[current=page]:bg-surface"
                to="/curator/requests"
              >
                Requests <PendingBadge count={pending} />
              </NavLink>
            </>
          )}
        </nav>
      )}
      <div className="ml-auto">
        {data?.person ? (
          <AccountMenu
            key={data.person.id}
            onboarded={onboarded}
            person={data.person}
            preview={preview}
            {...theme}
          />
        ) : (
          <ThemeToggle {...theme} />
        )}
      </div>
    </header>
  );
}

export function PendingBadge({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <span className="inline-flex min-w-5 items-center justify-center rounded-full bg-primary px-1.5 text-xs font-medium text-primary-foreground">
      {count}
      <span className="sr-only">{" pending"}</span>
    </span>
  );
}
