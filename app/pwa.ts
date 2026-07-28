export type Theme = "dark" | "light";

export const THEME_STORAGE_KEY = "memento-theme";
export const PWA_UPDATE_EVENT = "memento-pwa-update";
export const PWA_RESTART_GUARD_EVENT = "memento-pwa-restart-guard";

export interface PWARestartGuardDetail {
  blockedBy?: "dirty" | "saving";
}

const THEME_COLORS: Record<Theme, string> = {
  dark: "#020617",
  light: "#f0f9ff",
};

function browserThemeStorage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

export function readTheme(
  storage: Pick<Storage, "getItem"> | undefined = browserThemeStorage(),
): Theme {
  try {
    return storage?.getItem(THEME_STORAGE_KEY) === "light" ? "light" : "dark";
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

export function saveTheme(
  theme: Theme,
  storage: Pick<Storage, "setItem"> | undefined = browserThemeStorage(),
) {
  try {
    storage?.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // A blocked storage preference must not prevent Memento from opening.
  }
}

export function initializeTheme() {
  const theme = readTheme();
  applyTheme(theme, document);
  return theme;
}

export function registerServiceWorker(
  reload: () => void = () => window.location.reload(),
) {
  if (!("serviceWorker" in navigator) || !window.isSecureContext) return;

  window.addEventListener("load", () => {
    void navigator.serviceWorker
      .register("/service-worker.js")
      .then((registration) => {
        let updateAccepted = false;
        let reloading = false;
        const announcedWorkers = new WeakSet<ServiceWorker>();
        const restart = () => {
          if (reloading) return;
          reloading = true;
          reload();
        };
        const announceUpdate = (worker: ServiceWorker) => {
          if (announcedWorkers.has(worker)) return;
          announcedWorkers.add(worker);
          const acceptUpdate = () => {
            updateAccepted = true;
            if (
              worker.state === "activated" ||
              registration.active === worker ||
              navigator.serviceWorker.controller === worker
            ) {
              restart();
              return;
            }
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
          if (!updateAccepted) return;
          restart();
        });
      })
      .catch(() => {
        // The online application remains usable when installation is blocked.
      });
  });
}
