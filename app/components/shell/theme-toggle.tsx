import { useEffect, useState } from "react";

import { Button } from "../ui/button";

export function ThemeToggle() {
  const [theme, setTheme] = useState(() => {
    try {
      return window.localStorage.getItem("memento-theme") === "light"
        ? "light"
        : "dark";
    } catch {
      return "dark";
    }
  });
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      window.localStorage.setItem("memento-theme", theme);
    } catch {
      /* The switch still works when browser storage is unavailable. */
    }
  }, [theme]);
  return (
    <Button
      aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
      className="size-11 rounded-full p-0"
      onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
      variant="ghost"
    >
      <svg
        aria-hidden="true"
        fill="none"
        height="20"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.5"
        viewBox="0 0 24 24"
        width="20"
      >
        {theme === "dark" ? (
          <>
            <circle cx="12" cy="12" r="4" />
            <path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" />
          </>
        ) : (
          <path d="M20.8 14A9 9 0 0 1 10 3.2 9 9 0 1 0 20.8 14Z" />
        )}
      </svg>
    </Button>
  );
}
