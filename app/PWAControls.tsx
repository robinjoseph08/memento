import { useEffect, useState } from "react";

import {
  applyTheme,
  PWA_UPDATE_EVENT,
  readTheme,
  saveTheme,
  type Theme,
} from "./pwa";

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() =>
    readTheme(window.localStorage),
  );
  const nextTheme = theme === "dark" ? "light" : "dark";

  useEffect(() => {
    applyTheme(theme, document);
    saveTheme(theme, window.localStorage);
  }, [theme]);

  return (
    <button
      aria-label={`Use ${nextTheme} theme`}
      className="theme-toggle"
      onClick={() => setTheme(nextTheme)}
      type="button"
    >
      {theme === "dark" ? "Light" : "Dark"} theme
    </button>
  );
}

export function PWAUpdatePrompt() {
  const [acceptUpdate, setAcceptUpdate] = useState<(() => void) | null>(null);

  useEffect(() => {
    const handleUpdate = (event: Event) => {
      const updateEvent = event as CustomEvent<() => void>;
      setAcceptUpdate(() => updateEvent.detail);
    };
    window.addEventListener(PWA_UPDATE_EVENT, handleUpdate);
    return () => window.removeEventListener(PWA_UPDATE_EVENT, handleUpdate);
  }, []);

  if (!acceptUpdate) return null;
  return (
    <aside aria-live="polite" className="pwa-update">
      <span>A Memento update is ready.</span>
      <button onClick={acceptUpdate} type="button">
        Update and restart
      </button>
    </aside>
  );
}

export function OfflineNotice() {
  return (
    <section aria-labelledby="offline-title" className="offline-notice">
      <p className="step-label">Network unavailable</p>
      <h1 id="offline-title">Memento is offline</h1>
      <p>
        Reconnect to open your private library. Protected photos and account
        responses are never saved for offline viewing.
      </p>
    </section>
  );
}
