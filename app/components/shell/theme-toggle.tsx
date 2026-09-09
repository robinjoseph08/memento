import { Moon, Sun } from "lucide-react";

import type { useTheme } from "../../hooks/use-theme";
import { Button } from "../ui/button";

export function ThemeToggle({ theme, setTheme }: ReturnType<typeof useTheme>) {
  return (
    <Button
      aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
      className="size-11 rounded-full p-0"
      onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
      variant="ghost"
    >
      {theme === "dark" ? (
        <Sun aria-hidden="true" size={20} strokeWidth={1.5} />
      ) : (
        <Moon aria-hidden="true" size={20} strokeWidth={1.5} />
      )}
    </Button>
  );
}
