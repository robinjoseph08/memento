import "@testing-library/jest-dom/vitest";

import { beforeEach, vi } from "vitest";

// jsdom lacks pointer capture and scrolling. Their behavior is exercised in Playwright.
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

// cmdk measures its list with ResizeObserver, which jsdom does not implement.
// Assigned directly so tests that unstub globals keep it.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver =
  ResizeObserverStub as unknown as typeof ResizeObserver;

// jsdom has no layout engine. Responsive behavior is exercised in Playwright.
beforeEach(() => {
  vi.stubGlobal("matchMedia", (media: string) => ({
    media,
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
