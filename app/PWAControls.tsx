import { useEffect, useState } from "react";

import {
  applyTheme,
  PWA_RESTART_GUARD_EVENT,
  PWA_UPDATE_EVENT,
  readTheme,
  saveTheme,
  type PWARestartGuardDetail,
  type Theme,
} from "./pwa";

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => readTheme());
  const nextTheme = theme === "dark" ? "light" : "dark";

  useEffect(() => {
    applyTheme(theme, document);
    saveTheme(theme);
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
  const [blockedBy, setBlockedBy] =
    useState<PWARestartGuardDetail["blockedBy"]>();

  useEffect(() => {
    const handleUpdate = (event: Event) => {
      const updateEvent = event as CustomEvent<() => void>;
      setAcceptUpdate(() => updateEvent.detail);
      setBlockedBy(undefined);
    };
    window.addEventListener(PWA_UPDATE_EVENT, handleUpdate);
    return () => window.removeEventListener(PWA_UPDATE_EVENT, handleUpdate);
  }, []);

  if (!acceptUpdate) return null;
  const restart = () => {
    const detail: PWARestartGuardDetail = {};
    const allowed = window.dispatchEvent(
      new CustomEvent(PWA_RESTART_GUARD_EVENT, {
        cancelable: true,
        detail,
      }),
    );
    if (!allowed) {
      setBlockedBy(detail.blockedBy ?? "dirty");
      return;
    }
    setAcceptUpdate(null);
    setBlockedBy(undefined);
    acceptUpdate();
  };
  return (
    <aside aria-live="polite" className="pwa-update">
      <span>A Memento update is ready.</span>
      <button onClick={restart} type="button">
        Update and restart
      </button>
      {blockedBy === "saving" ? (
        <span>Wait for the current save to finish before restarting.</span>
      ) : null}
      {blockedBy === "dirty" ? (
        <span>Your unsaved changes were kept.</span>
      ) : null}
    </aside>
  );
}

export function OfflineNotice() {
  return (
    <section aria-labelledby="offline-title" className="offline-notice">
      <p className="step-label">Network unavailable</p>
      <h1 id="offline-title">Memento is offline</h1>
      <p>
        Reconnect to open your private library. Memento's offline cache does not
        store protected photos or account responses. Files you downloaded
        separately remain on this device.
      </p>
    </section>
  );
}
