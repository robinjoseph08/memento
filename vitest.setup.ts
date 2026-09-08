import "@testing-library/jest-dom/vitest";

import { beforeEach, vi } from "vitest";

// jsdom lacks pointer capture and scrolling. Their behavior is exercised in Playwright.
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

// jsdom has no layout engine. Responsive behavior is exercised in Playwright.
beforeEach(() => {
  vi.stubGlobal("matchMedia", (media: string) => ({
    media,
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
