import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { OfflineNotice, PWAUpdatePrompt, ThemeToggle } from "./PWAControls";
import {
  applyTheme,
  PWA_UPDATE_EVENT,
  readTheme,
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
});

test("offline copy says protected media is unavailable rather than empty", () => {
  render(<OfflineNotice />);

  expect(
    screen.getByRole("heading", { name: "Memento is offline" }),
  ).toBeInTheDocument();
  expect(screen.getByText(/never saved for offline viewing/)).toBeVisible();
  expect(screen.queryByText(/No photos/)).not.toBeInTheDocument();
});
