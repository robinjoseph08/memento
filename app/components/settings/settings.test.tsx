import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "../../App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const curator = {
  id: "robin",
  display_name: "Robin Curator",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};

const quiet = {
  needs_attention: {
    pending_requests: 0,
    imports: [],
    deliveries: [],
    chapters: [],
  },
  ready: { unpublished: [], unannounced_people: 0 },
  active: false,
};

// Each check answers on its own so a section can be re-checked alone.
function serveSettings(email: () => unknown, ffprobe: () => unknown) {
  const checks = { connection: 0, email: 0, ffprobe: 0 };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path === "/api/curator/connection") {
        checks.connection += 1;
        return Response.json({
          usable: true,
          version: "3.1.0",
          message: "Immich is connected.",
        });
      }
      if (path === "/api/curator/email") {
        checks.email += 1;
        return Response.json(email());
      }
      if (path === "/api/curator/ffprobe") {
        checks.ffprobe += 1;
        return Response.json(ffprobe());
      }
      if (path === "/api/curator/dashboard") return Response.json(quiet);
      if (path === "/api/access-requests") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  return checks;
}

it("shows email as quietly not configured and re-checks one integration at a time", async () => {
  const checks = serveSettings(
    () => ({
      configured: false,
      usable: false,
      sender: "",
      message: "Email is not configured, so Invitations cannot be sent.",
    }),
    () => ({
      usable: true,
      version: "7.1.1",
      message: "ffprobe is ready to read video chapters.",
    }),
  );
  window.history.replaceState(null, "", "/curator/settings");
  const user = userEvent.setup();
  render(<App />);

  const email = within(await screen.findByRole("region", { name: "Email" }));
  expect(await email.findByText("Not configured")).toHaveClass("text-muted");
  expect(
    email.getByText("Email is not configured, so Invitations cannot be sent."),
  ).toBeInTheDocument();
  expect(email.queryByText(/Sending as/)).not.toBeInTheDocument();

  const chapters = within(
    screen.getByRole("region", { name: "Video chapters" }),
  );
  expect(await chapters.findByText("Ready")).toHaveClass(
    "text-accent-foreground",
  );
  expect(chapters.getByText("ffprobe 7.1.1")).toBeInTheDocument();
  expect(
    within(screen.getByRole("region", { name: "Immich connection" })).getByText(
      "Connected",
    ),
  ).toBeInTheDocument();

  await user.click(email.getByRole("button", { name: "Check again" }));
  await email.findByText("Not configured");
  expect(checks).toEqual({ connection: 1, email: 2, ffprobe: 1 });
});

it("shows a configured mail server that refuses the connection as a failure", async () => {
  serveSettings(
    () => ({
      configured: true,
      usable: false,
      sender: "Memento <memento@example.test>",
      message: "The mail server replied 535.",
    }),
    () => ({
      usable: false,
      version: "",
      message: "ffprobe was not found or could not start.",
    }),
  );
  window.history.replaceState(null, "", "/curator/settings");
  render(<App />);

  const email = within(await screen.findByRole("region", { name: "Email" }));
  expect(await email.findByText("Not connected")).toHaveClass(
    "text-destructive",
  );
  expect(email.getByText("The mail server replied 535.")).toBeInTheDocument();
  expect(
    email.getByText("Sending as Memento <memento@example.test>"),
  ).toBeInTheDocument();
  const chapters = within(
    screen.getByRole("region", { name: "Video chapters" }),
  );
  expect(await chapters.findByText("Not ready")).toHaveClass(
    "text-destructive",
  );
  expect(chapters.queryByText(/ffprobe \d/)).not.toBeInTheDocument();
});
