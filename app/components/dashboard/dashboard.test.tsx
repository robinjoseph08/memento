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

const busy = {
  needs_attention: {
    pending_requests: 2,
    imports: [
      {
        id: "trip",
        title: "Road trip",
        status: "failed",
        message: "Import failed. Check the Immich connection.",
        ready: false,
      },
    ],
    deliveries: [
      {
        id: "d1",
        kind: "update",
        recipient: "alex@example.test",
        status: "failed",
        message: "The mail server replied 550.",
        attempts: 1,
        updated_at: "2026-06-10T15:30:00Z",
        person_id: "alex",
        person_name: "Alex",
        invitation_id: "",
      },
      {
        id: "d2",
        kind: "update",
        recipient: "sam@example.test",
        status: "uncertain",
        message: "Memento restarted while this email was being sent.",
        attempts: 1,
        updated_at: "2026-06-10T15:30:00Z",
        person_id: "sam",
        person_name: "Sam",
        invitation_id: "",
      },
      {
        id: "d3",
        kind: "invitation",
        recipient: "pat@example.test",
        status: "failed",
        message: "The mail server replied 550.",
        attempts: 1,
        updated_at: "2026-06-10T15:30:00Z",
        person_id: "pat",
        person_name: "Pat",
        invitation_id: "i1",
      },
    ],
    chapters: [
      {
        album_id: "coast",
        album_title: "Coast",
        moment_id: "m1",
        entry_id: "e1",
        title: "Surf lesson",
        message: "Chapter extraction failed.",
      },
    ],
  },
  ready: {
    unpublished: [
      {
        id: "coast",
        title: "Coast",
        status: "complete",
        message: "",
        ready: true,
      },
      {
        id: "family",
        title: "Family",
        status: "complete",
        message: "",
        ready: false,
      },
    ],
    unannounced_people: 3,
  },
  active: false,
};

it("groups work into Needs attention and Ready when you are, and retries email deliberately", async () => {
  const retries: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path === "/api/access-requests") return Response.json([]);
      if (path === "/api/curator/dashboard") return Response.json(busy);
      if (path === "/api/curator/connection")
        return Response.json({
          usable: false,
          version: "",
          message: "Immich is unavailable. Check its address.",
        });
      if (
        path.startsWith("/api/curator/notifications/deliveries/") &&
        path.endsWith("/retry")
      ) {
        retries.push(path.split("/")[5]);
        return Response.json({
          id: path.split("/")[5],
          status: "queued",
          attempts: 1,
          message: "",
          updated_at: "2026-06-10T15:40:00Z",
        });
      }
      throw new Error(`Unexpected request: ${path} ${options?.method ?? ""}`);
    }),
  );
  window.history.replaceState(null, "", "/curator");
  const user = userEvent.setup();
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "Hi, Robin" }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Memento");
  expect(screen.getByRole("link", { name: "Import an album" })).toHaveAttribute(
    "href",
    "/curator/import",
  );

  const attention = await screen.findByRole("region", {
    name: "Needs attention",
  });
  // The Immich check is its own request and lands after the work list.
  await within(attention).findByText("Immich is not connected");
  const items = within(attention).getAllByRole("listitem");
  expect(items).toHaveLength(7);
  expect(items[0]).toHaveTextContent("Immich is not connected");
  expect(items[0]).toHaveTextContent(
    "Immich is unavailable. Check its address.",
  );
  expect(
    within(items[0]).getByRole("link", { name: "Check the connection" }),
  ).toHaveAttribute("href", "/curator/settings");
  expect(items[1]).toHaveTextContent(
    "2 access requests waiting for a decision",
  );
  expect(
    within(items[1]).getByRole("link", { name: "Review requests" }),
  ).toHaveAttribute("href", "/curator/requests");
  expect(items[2]).toHaveTextContent("Road trip: Import failed");
  expect(
    within(items[2]).getByRole("button", { name: "Retry import" }),
  ).toBeVisible();
  expect(items[3]).toHaveTextContent("Update email for Alex");
  expect(items[3]).toHaveTextContent("Email not delivered · alex@example.test");
  expect(items[4]).toHaveTextContent(
    "Email delivery uncertain · sam@example.test",
  );
  expect(items[5]).toHaveTextContent("Invitation for Pat");
  expect(
    within(items[5]).getByRole("link", { name: "Open Pat" }),
  ).toHaveAttribute("href", "/curator/people/pat");
  expect(within(items[5]).queryByRole("button")).not.toBeInTheDocument();
  expect(items[6]).toHaveTextContent("Chapters for Surf lesson in Coast");
  expect(
    within(items[6]).getByRole("link", { name: "Open video" }),
  ).toHaveAttribute(
    "href",
    "/curator/albums/coast?moment=m1&entry=e1&pane=detail",
  );

  const ready = screen.getByRole("region", { name: "Ready when you are" });
  const waiting = within(ready).getAllByRole("listitem");
  expect(waiting).toHaveLength(3);
  expect(waiting[0]).toHaveTextContent(
    "3 people have new photos or videos to hear about",
  );
  expect(
    within(waiting[0]).getByRole("link", { name: "Send updates" }),
  ).toHaveAttribute("href", "/curator/updates");
  expect(waiting[1]).toHaveTextContent("Coast");
  expect(waiting[1]).toHaveTextContent("Publish whenever you like");
  expect(
    within(waiting[1]).getByRole("link", { name: "Review and publish" }),
  ).toHaveAttribute("href", "/curator/albums/coast");
  expect(waiting[2]).toHaveTextContent("Nobody has access yet");
  expect(
    within(waiting[2]).getByRole("link", { name: "Set up access" }),
  ).toHaveAttribute("href", "/curator/albums/family?section=access");
  expect(ready).not.toHaveTextContent(/fail/i);

  // A failed email sends again directly; an uncertain one warns first.
  await user.click(
    within(items[3]).getByRole("button", {
      name: "Send email to alex@example.test again",
    }),
  );
  expect(retries).toEqual(["d1"]);
  await user.click(
    within(items[4]).getByRole("button", {
      name: "Send email to sam@example.test again",
    }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Send email to sam@example.test again?",
  });
  expect(dialog).toHaveTextContent("could deliver a duplicate");
  await user.click(
    within(dialog).getByRole("button", {
      name: "Send email to sam@example.test again",
    }),
  );
  expect(retries).toEqual(["d1", "d2"]);
});

it("says plainly when nothing needs attention and nothing is waiting", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          auth_mode: "fake",
        });
      if (path === "/api/access-requests") return Response.json([]);
      if (path === "/api/curator/dashboard")
        return Response.json({
          needs_attention: {
            pending_requests: 0,
            imports: [],
            deliveries: [],
            chapters: [],
          },
          ready: { unpublished: [], unannounced_people: 0 },
          active: false,
        });
      if (path === "/api/curator/connection")
        return Response.json({
          usable: true,
          version: "3.1.0",
          message: "Immich is connected.",
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator");
  render(<App />);
  const attention = await screen.findByRole("region", {
    name: "Needs attention",
  });
  expect(
    await within(attention).findByText(
      "Nothing needs your attention right now.",
    ),
  ).toBeVisible();
  expect(within(attention).queryByText(/Immich/)).not.toBeInTheDocument();
  expect(
    screen.getByRole("region", { name: "Ready when you are" }),
  ).toHaveTextContent(
    "Everything is published and everyone has heard about it.",
  );
});
