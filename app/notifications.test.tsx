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
  status: "new",
  photo_count: 4,
  video_count: 1,
  video_titles: ["Surf lesson"],
};
const family = {
  id: "family",
  title: "Family",
  status: "updated",
  photo_count: 2,
  video_count: 0,
  video_titles: [],
};
const album = {
  id: "coast",
  title: "Coast",
  description: "",
  photo_count: 4,
  video_count: 1,
  start_date: "2026-06-01",
  end_date: "2026-06-03",
  cover_url: "",
  cover_preview_url: "",
  days: [],
};

it("shows the unread count, opens a one-Album update into that Album, and keeps browsing separate from read state", async () => {
  let notifications = [
    {
      id: "n2",
      created_at: "2026-06-10T15:30:00Z",
      read_at: null as string | null,
      albums: [coast, family],
      note: "",
    },
    {
      id: "n1",
      created_at: "2026-06-05T09:00:00Z",
      read_at: null as string | null,
      albums: [coast],
      note: "Finally got these together. Enjoy!",
    },
  ];
  const reads: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: member,
          auth_mode: "fake",
        });
      if (path === "/api/notifications")
        return Response.json({
          notifications,
          unread: notifications.filter((item) => !item.read_at).length,
        });
      if (path === "/api/notifications/read-all") {
        reads.push("all");
        notifications = notifications.map((item) => ({
          ...item,
          read_at: item.read_at ?? "2026-06-11T00:00:00Z",
        }));
        return Response.json({ notifications, unread: 0 });
      }
      if (path.startsWith("/api/notifications/") && path.endsWith("/read")) {
        const id = path.split("/")[3];
        reads.push(id);
        notifications = notifications.map((item) =>
          item.id === id ? { ...item, read_at: "2026-06-11T00:00:00Z" } : item,
        );
        return Response.json(notifications.find((item) => item.id === id));
      }
      if (path === "/api/albums") return Response.json([album]);
      if (path === "/api/albums/coast") return Response.json(album);
      if (path.startsWith("/api/albums/coast/"))
        return Response.json({ entries: [], next_cursor: "" });
      throw new Error(`Unexpected request: ${path} ${options?.method ?? ""}`);
    }),
  );
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  const bell = await screen.findByRole("button", { name: "Updates, 2 unread" });
  await user.click(bell);
  const panel = await screen.findByRole("dialog", { name: "Updates" });
  const rows = within(panel).getAllByRole("listitem");
  expect(rows).toHaveLength(2);
  // Newest first, with a date but no clock time.
  expect(rows[0]).toHaveTextContent("2 albums: Coast, Family");
  expect(rows[0]).toHaveTextContent("June 10, 2026");
  expect(rows[0]).not.toHaveTextContent(/\d:\d\d/);
  expect(rows[1]).toHaveTextContent("Coast");
  expect(rows[1]).toHaveTextContent("4 photos and 1 video · New album");
  expect(rows[1]).toHaveTextContent("Videos: Surf lesson");
  expect(rows[1]).toHaveTextContent("Finally got these together. Enjoy!");

  await user.click(within(rows[1]).getByRole("button", { name: "Open Coast" }));
  await waitFor(() =>
    expect(window.location.pathname).toBe("/albums/coast/photos"),
  );
  expect(reads).toEqual(["n1"]);
  expect(
    await screen.findByRole("button", { name: "Updates, 1 unread" }),
  ).toBeVisible();

  // Browsing the Album list changes nothing; the other update stays unread.
  await user.click(screen.getByRole("link", { name: "Albums" }));
  await waitFor(() => expect(window.location.pathname).toBe("/albums"));
  expect(reads).toEqual(["n1"]);
  await user.click(screen.getByRole("button", { name: "Updates, 1 unread" }));
  const remaining = await screen.findByRole("button", {
    name: "Open 2 albums: Coast, Family",
  });
  await user.click(remaining);
  await waitFor(() => expect(window.location.pathname).toBe("/albums"));
  expect(reads).toEqual(["n1", "n2"]);
  expect(await screen.findByRole("button", { name: "Updates" })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Updates" }));
  expect(
    await screen.findByRole("button", { name: "All caught up" }),
  ).toBeDisabled();
});

it("marks every update read at once", async () => {
  const notifications = [
    {
      id: "n1",
      created_at: "2026-06-05T09:00:00Z",
      read_at: null as string | null,
      albums: [coast],
      note: "",
    },
  ];
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: member,
          auth_mode: "fake",
        });
      if (path === "/api/notifications")
        return Response.json({ notifications, unread: 1 });
      if (path === "/api/notifications/read-all") {
        calls.push(path);
        return Response.json({
          notifications: notifications.map((item) => ({
            ...item,
            read_at: "2026-06-11T00:00:00Z",
          })),
          unread: 0,
        });
      }
      if (path === "/api/albums") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Updates, 1 unread" }),
  );
  await user.click(
    await screen.findByRole("button", { name: "Mark all as read" }),
  );
  expect(
    await screen.findByRole("button", { name: "All caught up" }),
  ).toBeDisabled();
  expect(calls).toEqual(["/api/notifications/read-all"]);
  expect(screen.getByRole("button", { name: "Updates" })).toBeVisible();
});

it("lets a Curator review recipients, leave out an Album update, add a note, and send", async () => {
  const preview = {
    people: [
      {
        person_id: "alex",
        display_name: "Alex",
        update_email: "alex@example.test",
        email_updates: true,
        email_eligible: true,
        albums: [coast, family],
        review_token: "alex-token",
      },
      {
        person_id: "sam",
        display_name: "Sam",
        update_email: "",
        email_updates: false,
        email_eligible: false,
        albums: [coast],
        review_token: "sam-token",
      },
    ],
  };
  const approvals: unknown[] = [];
  let previews = 0;
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
      if (path === "/api/curator/notifications/preview") {
        previews++;
        return Response.json(previews === 1 ? preview : { people: [] });
      }
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
              photo_count: 4,
              video_count: 1,
            },
            {
              person_id: "sam",
              display_name: "Sam",
              status: "skipped",
              message: "Their updates changed since this preview.",
              notification_id: "",
              album_count: 0,
              photo_count: 0,
              video_count: 0,
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
  expect(
    await screen.findByRole("heading", { name: "Send updates" }),
  ).toBeVisible();
  expect(document.title).toBe("Updates | Memento");
  expect(
    screen.queryByRole("button", { name: /^Updates/ }),
  ).not.toBeInTheDocument();
  const form = await screen.findByRole("form", { name: "Send updates" });
  const rows = within(form).getAllByRole("listitem");
  expect(rows).toHaveLength(2);
  expect(rows[0]).toHaveTextContent("Alex");
  expect(rows[0]).toHaveTextContent("Email to alex@example.test");
  expect(rows[0]).toHaveTextContent("2 albums");
  expect(rows[0]).toHaveTextContent("6 photos, 1 video");
  expect(rows[1]).toHaveTextContent("In app only, no email selected");
  expect(within(rows[1]).queryByText("Videos:")).not.toBeInTheDocument();

  await user.click(
    within(rows[0]).getByRole("button", { name: "Show albums for Alex" }),
  );
  const details = within(rows[0]).getByRole("list");
  expect(details).toHaveTextContent("Coast · New album · 4 photos, 1 video");
  expect(details).toHaveTextContent("Videos: Surf lesson");
  expect(details).toHaveTextContent("Family · Updated · 2 photos, 0 videos");
  await user.click(
    within(details).getByRole("checkbox", { name: "Include Family for Alex" }),
  );
  expect(rows[0]).toHaveTextContent("1 album (1 left out)");
  expect(rows[0]).toHaveTextContent("4 photos, 1 video");
  expect(details).toHaveTextContent("Left out");

  await user.type(
    screen.getByRole("textbox", { name: "Note for everyone (optional)" }),
    "Enjoy!",
  );
  await user.click(
    screen.getByRole("button", { name: "Send updates to 2 people" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Updates sent to 1 person" }),
  ).toBeVisible();
  expect(approvals).toEqual([
    {
      note: "Enjoy!",
      people: [
        {
          person_id: "alex",
          review_token: "alex-token",
          excluded_album_ids: ["family"],
        },
        { person_id: "sam", review_token: "sam-token", excluded_album_ids: [] },
      ],
    },
  ]);
  expect(screen.getByRole("status")).toHaveTextContent(
    "Each person now has an update in Memento.",
  );
  const results = within(
    screen.getByRole("region", { name: "Updates sent to 1 person" }),
  ).getAllByRole("listitem");
  expect(results[0]).toHaveTextContent("Alex · 1 album, 4 photos, 1 video");
  expect(results[1]).toHaveTextContent(
    "Sam · Not sent. Their updates changed since this preview.",
  );
  await user.click(screen.getByRole("button", { name: "Check again" }));
  expect(
    await screen.findByRole("heading", { name: "Everyone is up to date" }),
  ).toBeVisible();
  expect(previews).toBe(2);
});

it("keeps an update unread and stays put when marking it read fails", async () => {
  const notifications = [
    {
      id: "n1",
      created_at: "2026-06-05T09:00:00Z",
      read_at: null as string | null,
      albums: [coast],
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
          auth_mode: "fake",
        });
      if (path === "/api/notifications")
        return Response.json({ notifications, unread: 1 });
      if (path === "/api/notifications/n1/read")
        return Response.json(
          { error: { message: "Server exploded", code: "internal" } },
          { status: 500 },
        );
      if (path === "/api/albums") return Response.json([]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/albums");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Updates, 1 unread" }),
  );
  await user.click(await screen.findByRole("button", { name: "Open Coast" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong. Please try again.",
  );
  expect(window.location.pathname).toBe("/albums");
  expect(
    screen.getByRole("button", { name: "Updates, 1 unread" }),
  ).toBeVisible();
});
