import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const member = {
  id: "alex",
  display_name: "Alex",
  is_curator: false,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
  update_email: "alex@example.test",
  email_updates: true,
  avatar_url: "",
};
const curator = {
  id: "robin",
  display_name: "Robin",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};
const coast = {
  id: "coast",
  title: "Coast",
  status: "updated",
  photo_count: 2,
  video_count: 0,
};

function offered(id: string, title: string, photos: number) {
  return {
    id,
    title,
    status: "offered",
    photo_count: photos,
    video_count: 0,
  };
}

it("lets a Curator review Albums new to view apart from own Album changes and leave one out", async () => {
  const preview = {
    people: [
      {
        person_id: "alex",
        display_name: "Alex",
        update_email: "alex@example.test",
        email_updates: true,
        email_eligible: true,
        albums: [coast],
        offered_albums: [
          offered("wedding", "Wedding", 40),
          offered("party", "Party", 3),
        ],
        more_offered_albums: 6,
        review_token: "alex-token",
      },
    ],
    email_configured: true,
  };
  const approvals: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          sign_in_methods: ["fake"],
        });
      if (path === "/api/access-requests") return Response.json([]);
      if (path === "/api/curator/notifications/preview")
        return Response.json(preview);
      if (path === "/api/curator/notifications/approve") {
        approvals.push(JSON.parse(String(options?.body)));
        return Response.json({
          people: [
            {
              person_id: "alex",
              display_name: "Alex",
              status: "notified",
              message: "",
              notification_id: "n1",
              album_count: 1,
              photo_count: 2,
              video_count: 0,
              offered_album_count: 7,
              email: "",
              delivery: null,
            },
          ],
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/updates");
  const user = userEvent.setup();
  render(<App />);
  const form = await screen.findByRole("form", { name: "Send updates" });
  const row = within(form).getAllByRole("listitem")[0];
  expect(row).toHaveTextContent("1 album");
  expect(row).toHaveTextContent("8 new albums to view");

  await user.click(
    within(row).getByRole("button", { name: "Show albums for Alex" }),
  );
  const section = within(row).getByRole("region", {
    name: "New albums Alex can view",
  });
  expect(section).toHaveTextContent("New albums you can view");
  expect(section).toHaveTextContent("Wedding · New to view · 40 photos");
  expect(section).toHaveTextContent("and 6 more albums");
  expect(section).not.toHaveTextContent("Coast");
  await user.click(
    within(section).getByRole("checkbox", { name: "Include Party for Alex" }),
  );
  expect(row).toHaveTextContent("7 new albums to view (1 left out)");

  await user.click(
    screen.getByRole("button", { name: "Send updates to 1 person" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Updates sent to 1 person" }),
  ).toBeVisible();
  expect(approvals).toEqual([
    {
      note: "",
      people: [
        {
          person_id: "alex",
          review_token: "alex-token",
          excluded_album_ids: [],
          excluded_offered_album_ids: ["party"],
        },
      ],
    },
  ]);
  expect(
    screen.getByRole("region", { name: "Updates sent to 1 person" }),
  ).toHaveTextContent(
    "Alex · 1 album, 2 photos, 0 videos, 7 new albums to view",
  );
});

it("shows a viewer the Albums they can now view and opens a single one's preview", async () => {
  const notifications = [
    {
      id: "n2",
      created_at: "2026-06-10T15:30:00Z",
      read_at: null as string | null,
      albums: [coast],
      offered_albums: [offered("wedding", "Wedding", 40)],
      more_offered_albums: 0,
      note: "",
    },
    {
      id: "n1",
      created_at: "2026-06-05T09:00:00Z",
      read_at: null as string | null,
      albums: [],
      offered_albums: [
        offered("wedding", "Wedding", 40),
        offered("party", "Party", 3),
      ],
      more_offered_albums: 4,
      note: "",
    },
    {
      id: "n0",
      created_at: "2026-06-01T09:00:00Z",
      read_at: null as string | null,
      albums: [],
      offered_albums: [offered("reunion", "Reunion", 12)],
      more_offered_albums: 0,
      note: "",
    },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: member,
          sign_in_methods: ["fake"],
        });
      if (path === "/api/notifications")
        return Response.json({ notifications, unread: 3 });
      if (path === "/api/notifications/n0/read")
        return Response.json({
          ...notifications[2],
          read_at: "2026-06-11T00:00:00Z",
        });
      if (path === "/api/albums" || path === "/api/albums/more")
        return Response.json([]);
      if (path.startsWith("/api/albums/reunion"))
        return Response.json(
          { error: { message: "Album not found.", code: "not_found" } },
          { status: 404 },
        );
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Updates, 3 unread" }),
  );
  const panel = await screen.findByRole("dialog", { name: "New updates" });
  const rows = within(panel).getAllByRole("listitem");
  expect(rows[0]).toHaveTextContent("Coast");
  // Like the email, a kind with nothing new is left out.
  expect(rows[0]).toHaveTextContent("2 photos · Updated");
  expect(rows[0]).not.toHaveTextContent("0 videos");
  expect(rows[0]).toHaveTextContent("New albums you can view: Wedding");
  expect(rows[1]).toHaveTextContent("6 new albums you can view");
  expect(rows[1]).toHaveTextContent("Wedding, Party and 4 more albums");
  expect(rows[2]).toHaveTextContent("Reunion");
  expect(rows[2]).toHaveTextContent("New album you can view");

  await user.click(
    within(rows[2]).getByRole("button", { name: "Open Reunion" }),
  );
  expect(window.location.pathname).toBe("/albums/reunion/preview/photos");
});
