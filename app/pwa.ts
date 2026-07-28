export type Theme = "dark" | "light";

export const THEME_STORAGE_KEY = "memento-theme";
export const PWA_UPDATE_EVENT = "memento-pwa-update";

const THEME_COLORS: Record<Theme, string> = {
  dark: "#020617",
  light: "#f0f9ff",
};

export function readTheme(storage: Pick<Storage, "getItem">): Theme {
  try {
    return storage.getItem(THEME_STORAGE_KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(theme: Theme, document: Document) {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  document
    .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    ?.setAttribute("content", THEME_COLORS[theme]);
}

export function saveTheme(theme: Theme, storage: Pick<Storage, "setItem">) {
  try {
    storage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // A blocked storage preference must not prevent Memento from opening.
  }
}

export function initializeTheme() {
  const theme = readTheme(window.localStorage);
  applyTheme(theme, document);
  return theme;
}

export function registerServiceWorker() {
  if (!("serviceWorker" in navigator) || !window.isSecureContext) return;

  window.addEventListener("load", () => {
    void navigator.serviceWorker
      .register("/service-worker.js")
      .then((registration) => {
        let updateAccepted = false;
        let reloading = false;
        const announceUpdate = (worker: ServiceWorker) => {
          const acceptUpdate = () => {
            updateAccepted = true;
            worker.postMessage({ type: "SKIP_WAITING" });
          };
          window.dispatchEvent(
            new CustomEvent(PWA_UPDATE_EVENT, { detail: acceptUpdate }),
          );
        };

        if (registration.waiting && navigator.serviceWorker.controller) {
          announceUpdate(registration.waiting);
        }
        registration.addEventListener("updatefound", () => {
          const worker = registration.installing;
          worker?.addEventListener("statechange", () => {
            if (
              worker.state === "installed" &&
              navigator.serviceWorker.controller
            ) {
              announceUpdate(worker);
            }
          });
        });
        navigator.serviceWorker.addEventListener("controllerchange", () => {
          if (!updateAccepted || reloading) return;
          reloading = true;
          window.location.reload();
        });
      })
      .catch(() => {
        // The online application remains usable when installation is blocked.
      });
  });
}
