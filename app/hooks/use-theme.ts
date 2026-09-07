import { useEffect, useState } from "react";

// The shell owns the active theme; local storage keeps the choice across visits.
export function useTheme() {
  const [theme, setTheme] = useState<"light" | "dark">(() => {
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
      // Theme changes still work when browser storage is unavailable.
    }
  }, [theme]);
  return { theme, setTheme };
}
