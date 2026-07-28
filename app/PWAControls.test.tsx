import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { OfflineNotice, PWAUpdatePrompt, ThemeToggle } from "./PWAControls";
import {
  applyTheme,
  initializeTheme,
  PWA_RESTART_GUARD_EVENT,
  PWA_UPDATE_EVENT,
  readTheme,
  registerServiceWorker,
  saveTheme,
  THEME_STORAGE_KEY,
} from "./pwa";
import { useOnlineStatus } from "./useOnlineStatus";

afterEach(() => {
  cleanup();
  localStorage.clear();
  document.documentElement.dataset.theme = "dark";
  document.documentElement.style.colorScheme = "";
  vi.restoreAllMocks();
});

test("dark is the stable default when no theme preference exists", () => {
  const themeColor = document.createElement("meta");
  themeColor.name = "theme-color";
  document.head.append(themeColor);
  expect(readTheme(localStorage)).toBe("dark");

  applyTheme("dark", document);

  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(document.documentElement.style.colorScheme).toBe("dark");
  expect(themeColor).toHaveAttribute("content", "#020617");
  themeColor.remove();
});

test("theme control applies and persists the light theme across mounts", () => {
  const first = render(<ThemeToggle />);

  fireEvent.click(screen.getByRole("button", { name: "Use light theme" }));

  expect(document.documentElement.dataset.theme).toBe("light");
  expect(document.documentElement.style.colorScheme).toBe("light");
  expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
  first.unmount();

  render(<ThemeToggle />);
  expect(
    screen.getByRole("button", { name: "Use dark theme" }),
  ).toBeInTheDocument();
  expect(document.documentElement.dataset.theme).toBe("light");
});

test("blocked preference storage safely keeps the dark default", () => {
  const blockedStorage = {
    getItem: vi.fn(() => {
      throw new DOMException("blocked", "SecurityError");
    }),
    setItem: vi.fn(() => {
      throw new DOMException("blocked", "SecurityError");
    }),
  };

  expect(readTheme(blockedStorage)).toBe("dark");
  expect(() => saveTheme("light", blockedStorage)).not.toThrow();
});

test("theme initialization survives a blocked storage getter", () => {
  const descriptor = Object.getOwnPropertyDescriptor(window, "localStorage");
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    get: () => {
      throw new DOMException("blocked", "SecurityError");
    },
  });

  try {
    expect(initializeTheme()).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  } finally {
    if (descriptor) Object.defineProperty(window, "localStorage", descriptor);
  }
});

function NetworkProbe() {
  return <p>{useOnlineStatus() ? "online" : "offline"}</p>;
}

test("network state responds to browser offline and online events", () => {
  render(<NetworkProbe />);
  expect(screen.getByText("online")).toBeInTheDocument();

  fireEvent.offline(window);
  expect(screen.getByText("offline")).toBeInTheDocument();

  fireEvent.online(window);
  expect(screen.getByText("online")).toBeInTheDocument();
});

test("an available update waits for explicit approval", () => {
  const acceptUpdate = vi.fn();
  render(<PWAUpdatePrompt />);
  expect(
    screen.queryByText("A Memento update is ready."),
  ).not.toBeInTheDocument();

  fireEvent(
    window,
    new CustomEvent(PWA_UPDATE_EVENT, { detail: acceptUpdate }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Update and restart" }));

  expect(acceptUpdate).toHaveBeenCalledOnce();
  expect(
    screen.queryByText("A Memento update is ready."),
  ).not.toBeInTheDocument();
});

test("an update restart remains pending when current work blocks it", () => {
  const acceptUpdate = vi.fn();
  const protectSavingWork = (event: Event) => {
    const guard = event as CustomEvent<{ blockedBy?: string }>;
    guard.detail.blockedBy = "saving";
    event.preventDefault();
  };
  window.addEventListener(PWA_RESTART_GUARD_EVENT, protectSavingWork);

  try {
    render(<PWAUpdatePrompt />);
    fireEvent(
      window,
      new CustomEvent(PWA_UPDATE_EVENT, { detail: acceptUpdate }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Update and restart" }));

    expect(acceptUpdate).not.toHaveBeenCalled();
    expect(
      screen.getByText(
        "Wait for the current save to finish before restarting.",
      ),
    ).toBeVisible();
    expect(screen.getByText("A Memento update is ready.")).toBeVisible();
  } finally {
    window.removeEventListener(PWA_RESTART_GUARD_EVENT, protectSavingWork);
  }
});

test("accepting an update already activated by another tab restarts immediately", async () => {
  const workerState = {
    state: "installed",
    postMessage: vi.fn(),
  };
  const worker = workerState as unknown as ServiceWorker;
  const oldWorker = {} as ServiceWorker;
  const registrationState: {
    active: ServiceWorker;
    waiting: ServiceWorker | null;
    addEventListener: ReturnType<typeof vi.fn>;
  } = {
    active: oldWorker,
    waiting: worker,
    addEventListener: vi.fn(),
  };
  const registration =
    registrationState as unknown as ServiceWorkerRegistration;
  const serviceWorkerState = {
    controller: oldWorker,
    register: vi.fn().mockResolvedValue(registration),
    addEventListener: vi.fn(),
  };
  const serviceWorkers =
    serviceWorkerState as unknown as ServiceWorkerContainer;
  const serviceWorkerDescriptor = Object.getOwnPropertyDescriptor(
    navigator,
    "serviceWorker",
  );
  const secureContextDescriptor = Object.getOwnPropertyDescriptor(
    window,
    "isSecureContext",
  );
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: serviceWorkers,
  });
  Object.defineProperty(window, "isSecureContext", {
    configurable: true,
    value: true,
  });
  let acceptUpdate: (() => void) | undefined;
  const captureUpdate = (event: Event) => {
    acceptUpdate = (event as CustomEvent<() => void>).detail;
  };
  window.addEventListener(PWA_UPDATE_EVENT, captureUpdate);
  const reload = vi.fn();

  try {
    registerServiceWorker(reload);
    fireEvent.load(window);
    await vi.waitFor(() => expect(acceptUpdate).toBeTypeOf("function"));

    workerState.state = "activated";
    registrationState.active = worker;
    registrationState.waiting = null;
    serviceWorkerState.controller = worker;
    acceptUpdate?.();

    expect(reload).toHaveBeenCalledOnce();
    expect(workerState.postMessage).not.toHaveBeenCalled();
  } finally {
    window.removeEventListener(PWA_UPDATE_EVENT, captureUpdate);
    if (serviceWorkerDescriptor) {
      Object.defineProperty(
        navigator,
        "serviceWorker",
        serviceWorkerDescriptor,
      );
    } else {
      Reflect.deleteProperty(navigator, "serviceWorker");
    }
    if (secureContextDescriptor) {
      Object.defineProperty(window, "isSecureContext", secureContextDescriptor);
    } else {
      Reflect.deleteProperty(window, "isSecureContext");
    }
  }
});

test("offline copy says protected media is unavailable rather than empty", () => {
  render(<OfflineNotice />);

  expect(
    screen.getByRole("heading", { name: "Memento is offline" }),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/Memento's offline cache does not store protected photos/),
  ).toBeVisible();
  expect(screen.getByText(/downloaded separately remain/)).toBeVisible();
  expect(screen.queryByText(/No photos/)).not.toBeInTheDocument();
});
