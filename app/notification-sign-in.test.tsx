import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("keeps one working notification bell across sign-out and sign-in without a reload", async () => {
  const person = {
    id: "alex",
    display_name: "Alex",
    is_curator: false,
    onboarding_completed_at: "2026-01-01T00:00:00Z",
    update_email: "alex@example.test",
    email_updates: true,
    avatar_url: "",
  };
  let signedIn = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/identity/status")
        return Response.json({
          claimed: true,
          auth_mode: "fake",
          person: signedIn ? person : null,
        });
      if (path === "/api/identity/fake-sign-in") {
        signedIn = true;
        return Response.json(person);
      }
      if (path === "/api/identity/sign-out") {
        signedIn = false;
        return new Response(null, { status: 204 });
      }
      if (path === "/api/albums") return Response.json([]);
      if (path === "/api/notifications")
        return Response.json({
          unread: 1,
          notifications: [
            {
              id: "n1",
              created_at: "2026-06-10T00:00:00Z",
              read_at: null,
              note: "New photos",
              albums: [
                {
                  id: "coast",
                  title: "Coast",
                  status: "new",
                  photo_count: 1,
                  video_count: 0,
                },
              ],
            },
          ],
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/sign-in");
  const user = userEvent.setup();
  render(<App />);
  await user.click(await screen.findByRole("button", { name: "Sign in" }));
  await screen.findByRole("button", { name: "Updates, 1 unread" });
  await user.click(screen.getByRole("button", { name: "Account menu" }));
  await user.click(screen.getByRole("menuitem", { name: "Sign out" }));
  const signIn = await screen.findByRole("button", {
    name: "Sign in",
  });
  expect(
    within(screen.getByRole("banner")).queryByRole("button", {
      name: /^Updates/,
    }),
  ).not.toBeInTheDocument();
  await user.click(signIn);
  await screen.findByRole("button", { name: "Updates, 1 unread" });
  const header = within(screen.getByRole("banner"));
  await waitFor(() =>
    expect(header.getAllByRole("button", { name: /^Updates/ })).toHaveLength(1),
  );
  await user.click(header.getByRole("button", { name: "Updates, 1 unread" }));
  expect(
    await screen.findByRole("dialog", { name: "New updates" }),
  ).toHaveTextContent("New photos");
});
