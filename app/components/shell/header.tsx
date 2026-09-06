import { Link } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import { useTheme } from "../../hooks/use-theme";
import { AccountMenu } from "./account-menu";
import { ThemeToggle } from "./theme-toggle";

export function Header() {
  const { data } = useIdentityStatus();
  const theme = useTheme();
  return (
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
      {data?.person ? (
        <AccountMenu key={data.person.id} person={data.person} {...theme} />
      ) : (
        <ThemeToggle {...theme} />
      )}
    </header>
  );
}
