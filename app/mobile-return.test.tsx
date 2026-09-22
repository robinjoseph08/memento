import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const returnTo = "memento://sign-in";
const returnPath = `/api/identity/mobile/return?return_to=${encodeURIComponent(returnTo)}`;
const member = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  update_email: "alex@example.test",
  email_updates: false,
  avatar_url: "",
};

function serve(initial: Record<string, unknown> | null, authMode = "fake") {
  let person = initial;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({ claimed: true, person, auth_mode: authMode });
      if (path.endsWith("/fake-sign-in")) {
        person = { ...member, onboarding_completed_at: "2026-02-01T00:00:00Z" };
        return Response.json(person);
      }
      if (path.endsWith("/identity/profile"))
        return Response.json({ person, identities: [] });
      if (path.endsWith("/identity/onboarding")) {
        person = {
          ...member,
          ...JSON.parse(String(options?.body)),
          onboarding_completed_at: "2026-02-01T00:00:00Z",
        };
        return Response.json(person);
      }
      if (path === "/api/albums") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
}

it("hands a signed-in Person back to the Mobile App after the fake sign-in", async () => {
  serve(null);
  window.history.replaceState(null, "", `/sign-in?return_to=${returnTo}`);
  const user = userEvent.setup();
  render(<App />);

  await user.click(await screen.findByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("status")).toHaveTextContent(
    "Opening the Memento app…",
  );
  expect(
    screen.getByRole("link", { name: "Open the Memento app" }),
  ).toHaveAttribute("href", returnPath);
  expect(document.title).toBe("Opening the app | Memento");
});

it("finishes Onboarding in the browser before returning to the app", async () => {
  serve(member);
  // Google sign-in lands on the home page with the return link preserved.
  window.history.replaceState(null, "", `/?return_to=${returnTo}`);
  const user = userEvent.setup();
  render(<App />);

  expect(
    await screen.findByRole("heading", { name: "Welcome to Memento" }),
  ).toBeVisible();
  expect(new URLSearchParams(window.location.search).get("return_to")).toBe(
    returnTo,
  );
  await user.click(
    await screen.findByRole("button", { name: "Continue to Memento" }),
  );

  expect(await screen.findByRole("status")).toHaveTextContent(
    "Opening the Memento app…",
  );
  expect(
    screen.getByRole("link", { name: "Open the Memento app" }),
  ).toHaveAttribute("href", returnPath);
});

it("sends the return link along to Google sign-in", async () => {
  serve(null, "google");
  window.history.replaceState(null, "", `/sign-in?return_to=${returnTo}`);
  render(<App />);

  expect(
    await screen.findByRole("link", { name: "Continue with Google" }),
  ).toHaveAttribute(
    "href",
    `/api/identity/google/start?return_to=${encodeURIComponent(returnTo)}`,
  );
});

it("keeps the web's own sign-in unchanged without a return link", async () => {
  serve(null);
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);

  await user.click(await screen.findByRole("button", { name: "Sign in" }));

  expect(
    await screen.findByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  expect(window.location.pathname).toBe("/albums");
});
