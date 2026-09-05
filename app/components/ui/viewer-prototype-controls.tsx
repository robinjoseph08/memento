import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";

import { Icon } from "../pages/viewer-prototype/artwork";

// Throwaway presentation controls for the selected viewer design.
export function ViewerPrototypeControls({ theme }: { theme: string }) {
  const [params, setParams] = useSearchParams();
  const panelRef = useRef<HTMLDialogElement>(null);
  const set = (key: string, value: string) =>
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.set(key, value);
        return next;
      },
      { replace: true },
    );

  useEffect(() => {
    console.info("Viewer prototype", {
      cover: "plain",
      logo: "Frames",
      wordmark: "memento",
      theme,
      emptyTab: params.get("empty") ?? "none",
      route: window.location.pathname,
    });
  }, [params, theme]);

  return (
    <>
      <aside className="prototype-switcher" aria-label="Prototype controls">
        <button
          className="prototype-current"
          onClick={() => panelRef.current?.showModal()}
          aria-haspopup="dialog"
        >
          <span>Throwaway prototype</span>
          <strong>Viewer preview</strong>
        </button>
        <span className="prototype-divider" />
        <button
          aria-label={`Use ${theme === "dark" ? "light" : "dark"} theme`}
          onClick={() => set("theme", theme === "dark" ? "light" : "dark")}
        >
          <Icon name={theme === "dark" ? "sun" : "moon"} />
        </button>
        <button
          aria-label="Preview settings"
          onClick={() => panelRef.current?.showModal()}
        >
          <Icon name="settings" />
        </button>
      </aside>
      <dialog
        ref={panelRef}
        className="prototype-settings"
        aria-labelledby="preview-title"
        onClick={(event) => {
          if (event.target === event.currentTarget) panelRef.current?.close();
        }}
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="study-kicker">Throwaway prototype</p>
            <h2 id="preview-title">Preview settings</h2>
          </div>
          <button
            className="icon-button"
            aria-label="Close preview settings"
            onClick={() => panelRef.current?.close()}
          >
            <Icon name="close" />
          </button>
        </div>
        <p className="study-intro">
          All images and clips are generated illustrations. Album details and
          chapters are fictional.
        </p>
        <form
          className="study-selects"
          onSubmit={(event) => event.preventDefault()}
        >
          <label>
            Presentation
            <select
              value={theme}
              onChange={(event) => set("theme", event.target.value)}
            >
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </label>
          <label>
            Album example
            <select
              value={params.get("empty") ?? "none"}
              onChange={(event) => set("empty", event.target.value)}
            >
              <option value="none">Photos and videos</option>
              <option value="videos">No videos</option>
              <option value="photos">No photos</option>
            </select>
          </label>
        </form>
      </dialog>
    </>
  );
}
