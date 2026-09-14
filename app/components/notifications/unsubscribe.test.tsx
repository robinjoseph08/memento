import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("shows the unsubscribe confirmation without signing in and changes nothing until confirmed", async () => {
  let subscribed = true;
  const posts: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: null,
          auth_mode: "fake",
        });
      if (path === "/api/unsubscribe?token=private-token") {
        if (options?.method === "POST") {
          posts.push(path);
          subscribed = false;
        }
        return Response.json({
          display_name: "Alex",
          email: "alex@example.test",
          subscribed,
        });
      }
      if (path === "/api/unsubscribe?token=stale-token")
        return Response.json(
          { error: { code: "not_found", message: "Link not found." } },
          { status: 404 },
        );
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/unsubscribe?token=private-token");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Stop update emails?" }),
  ).toBeVisible();
  expect(document.title).toBe("Unsubscribe | Memento");
  expect(screen.getByText(/Nothing changes until you confirm/)).toBeVisible();
  expect(posts).toEqual([]);
  await user.click(screen.getByRole("button", { name: "Stop update emails" }));
  expect(
    await screen.findByRole("heading", { name: "Update emails are off" }),
  ).toBeVisible();
  expect(posts).toEqual(["/api/unsubscribe?token=private-token"]);
  expect(screen.getByRole("status")).toHaveTextContent(
    "alex@example.test will no longer get emails",
  );
  expect(
    screen.getByRole("link", { name: "Sign in to Memento" }),
  ).toHaveAttribute("href", "/sign-in");

  window.history.replaceState(null, "", "/unsubscribe?token=stale-token");
  cleanup();
  render(<App />);
  expect(
    await screen.findByRole("heading", {
      name: "This link is no longer valid",
    }),
  ).toBeVisible();
});
