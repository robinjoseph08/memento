import { Link, NavLink } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { useTheme } from "../../hooks/use-theme";
import { AccountMenu } from "./account-menu";
import { ThemeToggle } from "./theme-toggle";

export function Header() {
  const { data } = useIdentityStatus();
  const theme = useTheme();
  return (
    <header className="flex min-h-16 flex-wrap items-center justify-between gap-4 border-b border-border px-4 py-2 min-[381px]:px-5 min-[761px]:px-8">
      <Link
        aria-label="memento home"
        className="inline-flex shrink-0 cursor-pointer touch-manipulation items-center gap-1.75 [-webkit-tap-highlight-color:transparent] focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-5 focus-visible:outline-ring min-[381px]:gap-2.75"
        to="/"
      >
        <svg
          aria-hidden="true"
          className="size-[29px] text-primary min-[381px]:size-9"
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
        <span className="font-heading text-[26px] leading-none tracking-[-0.7px] min-[381px]:text-[29px]">
          memento
        </span>
      </Link>
      {data?.person && (
        <nav
          aria-label="Main navigation"
          className="order-3 flex w-full gap-2 border-t border-border pt-2 min-[601px]:order-none min-[601px]:w-auto min-[601px]:flex-1 min-[601px]:border-0 min-[601px]:pt-0 min-[601px]:pl-8"
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
      {data?.person ? (
        <AccountMenu key={data.person.id} person={data.person} {...theme} />
      ) : (
        <ThemeToggle {...theme} />
      )}
    </header>
  );
}
