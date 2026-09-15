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
import type { DismissRequest, Preview } from "./types/generated/notifications";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const preview: Preview = {
  email_configured: true,
  people: [
    {
      person_id: "alex",
      display_name: "Alex",
      update_email: "alex@example.test",
      email_updates: true,
      email_eligible: true,
      review_token: "alex-token",
      albums: [
        {
          id: "coast",
          title: "Coast",
          status: "new",
          photo_count: 4,
          video_count: 1,
        },
        {
          id: "family",
          title: "Family",
          status: "updated",
          photo_count: 2,
          video_count: 0,
        },
      ],
    },
    {
      person_id: "sam",
      display_name: "Sam",
      update_email: "",
      email_updates: false,
      email_eligible: false,
      review_token: "sam-token",
      albums: [
        {
          id: "coast",
          title: "Coast",
          status: "new",
          photo_count: 4,
          video_count: 1,
        },
      ],
    },
  ],
};

function setup(
  dismiss: (body: DismissRequest) => Promise<Response>,
  fetchPreview?: () => Promise<Response>,
) {
  const calls: string[] = [];
  let currentPreview = preview;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options?: RequestInit) => {
      calls.push(path);
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: {
            id: "robin",
            display_name: "Robin",
            is_curator: true,
            onboarding_completed_at: "2026-01-01T00:00:00Z",
          },
          auth_mode: "fake",
        });
      if (path === "/api/access-requests") return Response.json([]);
      if (path === "/api/curator/notifications/preview")
        return fetchPreview ? fetchPreview() : Response.json(currentPreview);
      if (path === "/api/curator/notifications/dismiss")
        return dismiss(JSON.parse(String(options?.body)) as DismissRequest);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/updates");
  render(<App />);
  return {
    calls,
    user: userEvent.setup(),
    setPreview: (next: Preview) => {
      currentPreview = next;
    },
  };
}

const dismissed = {
  person_id: "alex",
  display_name: "Alex",
  status: "dismissed",
  message: "",
  notification_id: "",
  album_count: 1,
  photo_count: 4,
  video_count: 1,
  email: "",
  delivery: null,
};

it("confirms dismissal of only selected updates without sending and keeps the unsent note", async () => {
  const requests: DismissRequest[] = [];
  const { user, calls, setPreview } = setup(async (body) => {
    requests.push(body);
    return Response.json({ people: [dismissed] });
  });
  const trigger = await screen.findByRole("button", {
    name: "Dismiss selected updates",
  });
  await user.click(screen.getByRole("checkbox", { name: "Include Sam" }));
  await user.click(
    screen.getByRole("button", { name: "Show albums for Alex" }),
  );
  await user.click(
    screen.getByRole("checkbox", { name: "Include Family for Alex" }),
  );
  await user.type(
    screen.getByRole("textbox", { name: "Note for everyone (optional)" }),
    "Save for later",
  );

  await user.click(trigger);
  let dialog = await screen.findByRole("dialog", {
    name: "Dismiss selected updates?",
  });
  expect(dialog).toHaveTextContent("selected updates for 1 person");
  expect(dialog).toHaveTextContent("Their access stays the same");
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(requests).toEqual([]);
  expect(trigger).toHaveFocus();

  await user.click(trigger);
  dialog = await screen.findByRole("dialog", {
    name: "Dismiss selected updates?",
  });
  await user.click(
    within(dialog).getByRole("button", { name: "Dismiss selected updates" }),
  );
  expect(
    await screen.findByRole("heading", {
      name: "Updates dismissed for 1 person",
    }),
  ).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent(
    "No notifications or emails were sent. Access is unchanged.",
  );
  await waitFor(() =>
    expect(
      screen.getByRole("heading", { name: "Updates dismissed for 1 person" }),
    ).toHaveFocus(),
  );
  expect(requests).toEqual([
    {
      people: [
        {
          person_id: "alex",
          review_token: "alex-token",
          excluded_album_ids: ["family"],
        },
      ],
    },
  ]);
  expect(calls).not.toContain("/api/curator/notifications/approve");
  expect(calls.some((path) => path.includes("/deliveries"))).toBe(false);

  setPreview({
    ...preview,
    people: [
      {
        ...preview.people[0],
        albums: [preview.people[0].albums[1]],
        review_token: "remaining-token",
      },
      preview.people[1],
    ],
  });
  await user.click(screen.getByRole("button", { name: "Check again" }));
  expect(
    await screen.findByRole("textbox", {
      name: "Note for everyone (optional)",
    }),
  ).toHaveValue("Save for later");
  await waitFor(() =>
    expect(calls.filter((path) => path.endsWith("/preview"))).toHaveLength(2),
  );
  await user.click(
    screen.getByRole("button", { name: "Show albums for Alex" }),
  );
  expect(
    await screen.findByRole("checkbox", { name: "Include Family for Alex" }),
  ).toBeChecked();
  expect(
    screen.queryByRole("checkbox", { name: "Include Coast for Alex" }),
  ).not.toBeInTheDocument();
});

it("preserves selections after a failed dismissal and prevents duplicate submissions while pending", async () => {
  const requests: DismissRequest[] = [];
  let finish: (response: Response) => void = () => {};
  const { user, calls } = setup(async (body) => {
    requests.push(body);
    if (requests.length === 1)
      return Response.json(
        { error: { message: "Unavailable", code: "internal" } },
        { status: 500 },
      );
    return new Promise<Response>((resolve) => {
      finish = resolve;
    });
  });
  const trigger = await screen.findByRole("button", {
    name: "Dismiss selected updates",
  });
  await user.click(screen.getByRole("checkbox", { name: "Include Sam" }));
  await user.click(
    screen.getByRole("button", { name: "Show albums for Alex" }),
  );
  await user.click(
    screen.getByRole("checkbox", { name: "Include Family for Alex" }),
  );
  await user.click(trigger);
  const dialog = await screen.findByRole("dialog", {
    name: "Dismiss selected updates?",
  });
  const confirm = within(dialog).getByRole("button", {
    name: "Dismiss selected updates",
  });
  await user.click(confirm);
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(
    "Something went wrong. Please try again.",
  );
  await user.click(confirm);
  expect(confirm).toBeDisabled();
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
  expect(
    screen.getByRole("button", {
      name: "Send updates to 1 person",
      hidden: true,
    }),
  ).toBeDisabled();
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  finish(Response.json({ people: [dismissed] }));
  expect(
    await screen.findByRole("heading", {
      name: "Updates dismissed for 1 person",
    }),
  ).toBeVisible();
  expect(calls).not.toContain("/api/curator/notifications/approve");
});

it("disables dismissal when nothing is selected and explains stale previews without claiming success", async () => {
  const { user, calls, setPreview } = setup(async () =>
    Response.json({
      people: [
        {
          ...dismissed,
          status: "skipped",
          message:
            "Their updates changed since this preview. Review the updates again.",
        },
      ],
    }),
  );
  const trigger = await screen.findByRole("button", {
    name: "Dismiss selected updates",
  });
  await user.click(screen.getByRole("checkbox", { name: "Include Sam" }));
  await user.click(
    screen.getByRole("button", { name: "Show albums for Alex" }),
  );
  await user.click(
    screen.getByRole("checkbox", { name: "Include Family for Alex" }),
  );
  await user.click(
    screen.getByRole("checkbox", { name: "Include Coast for Alex" }),
  );
  expect(trigger).toBeDisabled();
  await user.click(
    screen.getByRole("checkbox", { name: "Include Coast for Alex" }),
  );
  await user.click(trigger);
  const dialog = await screen.findByRole("dialog", {
    name: "Dismiss selected updates?",
  });
  await user.click(
    within(dialog).getByRole("button", { name: "Dismiss selected updates" }),
  );
  expect(
    await screen.findByRole("heading", { name: "No updates were dismissed" }),
  ).toBeVisible();
  expect(screen.getByRole("listitem")).toHaveTextContent(
    "Alex · Not dismissed. Their updates changed since this preview.",
  );
  expect(calls).not.toContain("/api/curator/notifications/approve");
  setPreview({ people: [], email_configured: true });
  await user.click(screen.getByRole("button", { name: "Check again" }));
  expect(
    await screen.findByRole("heading", { name: "Everyone is up to date" }),
  ).toBeVisible();
});

it("waits for a refreshed preview before allowing dismissal confirmation", async () => {
  const requests: DismissRequest[] = [];
  let previews = 0;
  let finish: (response: Response) => void = () => {};
  const { user } = setup(
    async (body) => {
      requests.push(body);
      return Response.json({ people: [dismissed] });
    },
    async () => {
      if (++previews === 1) return Response.json(preview);
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    },
  );
  const trigger = await screen.findByRole("button", {
    name: "Dismiss selected updates",
  });
  await user.click(screen.getByRole("button", { name: "Check again" }));
  expect(trigger).toBeDisabled();
  await user.click(trigger);
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  const refreshedPerson = {
    ...preview.people[0],
    review_token: "fresh-token",
    albums: [{ ...preview.people[0].albums[0], photo_count: 5 }],
  };
  finish(Response.json({ ...preview, people: [refreshedPerson] }));
  await waitFor(() => expect(trigger).toBeEnabled());
  expect(screen.getByRole("listitem")).toHaveTextContent("5 photos, 1 video");
  await user.click(trigger);
  const dialog = await screen.findByRole("dialog", {
    name: "Dismiss selected updates?",
  });
  await user.click(
    within(dialog).getByRole("button", { name: "Dismiss selected updates" }),
  );
  expect(
    await screen.findByRole("heading", {
      name: "Updates dismissed for 1 person",
    }),
  ).toBeVisible();
  expect(requests).toEqual([
    {
      people: [
        {
          person_id: "alex",
          review_token: "fresh-token",
          excluded_album_ids: [],
        },
      ],
    },
  ]);
});
