import { expect, test, type Page } from "@playwright/test";

import type {
  Event as DraftEvent,
  MediaItem,
  OrganizeEventRequest,
} from "../../app/types/generated/events";

const csrfToken = "c".repeat(64);
const eventID = "11111111-1111-4111-8111-111111111111";
const momentOneID = "22222222-2222-4222-8222-222222222222";
const momentTwoID = "33333333-3333-4333-8333-333333333333";

function media(id: string, mediaType: string): MediaItem {
  return {
    id,
    media_type: mediaType,
    width: 1200,
    height: 800,
    local_date_time: null,
  };
}

const items = {
  first: media("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "first photo"),
  second: media("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "second photo"),
  third: media("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "third photo"),
  loose: media("dddddddd-dddd-4ddd-8ddd-dddddddddddd", "loose photo"),
};

function draft(version = 1): DraftEvent {
  return {
    id: eventID,
    lifecycle: "draft",
    title: "Family weekend",
    description: "",
    grouping_timezone: "UTC",
    version,
    final_review_complete: false,
    published_editable_version: null,
    sources: [],
    moments: [
      {
        id: momentOneID,
        title: "Friday",
        proposed_day: "2026-05-01",
        grouping_timezone: "UTC",
        source_days: ["2026-05-01"],
        proposal_kind: "local_day",
        cover_media_item_id: items.first.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.first],
      },
      {
        id: momentTwoID,
        title: "Saturday",
        proposed_day: "2026-05-02",
        grouping_timezone: "UTC",
        source_days: ["2026-05-02"],
        proposal_kind: "local_day",
        cover_media_item_id: items.second.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.second, items.third],
      },
    ],
    unassigned_media: [items.loose],
    withdrawals: [],
    created_at: "2026-05-03T00:00:00Z",
    updated_at: "2026-05-03T00:00:00Z",
  };
}

function eventFromRequest(
  request: OrganizeEventRequest,
  baseline = draft(request.version),
): DraftEvent {
  const byID = new Map(Object.values(items).map((item) => [item.id, item]));
  const priorMoments = new Map(
    baseline.moments.map((moment) => [moment.id, moment]),
  );
  const next = draft(request.version + 1);
  next.final_review_complete = request.final_review_complete;
  next.moments = request.moments.map((moment) => ({
    id: moment.id,
    title: moment.title ?? "",
    proposed_day: moment.proposed_day,
    grouping_timezone: "UTC",
    source_days: [moment.proposed_day],
    proposal_kind: "local_day",
    cover_media_item_id: moment.cover_media_item_id,
    attendance_complete:
      priorMoments.get(moment.id)?.attendance_complete ?? false,
    audience_complete: priorMoments.get(moment.id)?.audience_complete ?? false,
    media_items: moment.media_item_ids.map((id) => byID.get(id)!),
  }));
  next.unassigned_media = request.unassigned_media_ids.map((id) =>
    byID.get(id)!,
  );
  return next;
}

async function mockCuratorAPI(
  page: Page,
  outcomes: Array<"failed" | "conflict" | "success"> = [],
  initial = draft(),
) {
  let persisted = initial;
  const attempts: OrganizeEventRequest[] = [];
  let failureIndex = 0;

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "Setup not found." } },
      });
      return;
    }
    if (path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Robin",
          session_type: "public",
          csrf_token: csrfToken,
          curator: true,
        },
      });
      return;
    }
    if (path === "/api/events" && request.method() === "GET") {
      await route.fulfill({
        json: {
          events: [
            {
              id: eventID,
              lifecycle: persisted.lifecycle,
              title: persisted.title,
              version: persisted.version,
              moment_count: persisted.moments.length,
              unassigned_count: persisted.unassigned_media.length,
              updated_at: persisted.updated_at,
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/events/${eventID}` && request.method() === "GET") {
      await route.fulfill({ json: persisted });
      return;
    }
    if (path === "/api/withdrawals" && request.method() === "POST") {
      const body = request.postDataJSON() as {
        target_kind: string;
        target_id: string;
        reason: string;
      };
      persisted = {
        ...persisted,
        version: persisted.version + 1,
        final_review_complete: false,
        moments: persisted.moments.map((moment) => ({
          ...moment,
          audience_complete: false,
        })),
        withdrawals: [
          {
            id: "77777777-7777-4777-8777-777777777777",
            target_kind: body.target_kind,
            target_id: body.target_id,
            reason: body.reason,
            withdrawn_by_name: "Robin",
            withdrawn_at: "2026-05-03T00:00:00Z",
            restored_by_publication_id: null,
            restored_at: null,
            affected_recipient_count: 2,
            affected_media_count: 3,
            affected_event_count: 1,
          },
        ],
      };
      await route.fulfill({ status: 201, json: persisted.withdrawals[0] });
      return;
    }
    if (
      path === `/api/events/${eventID}/publications` &&
      request.method() === "POST"
    ) {
      persisted = {
        ...persisted,
        lifecycle: "published",
        published_editable_version: persisted.version,
      };
      await route.fulfill({
        status: 201,
        json: {
          id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
          event_id: eventID,
          revision: 1,
          editable_version: persisted.version,
          notify_recipients: true,
          committed_at: "2026-05-03T00:00:00Z",
        },
      });
      return;
    }
    if (path === `/api/events/${eventID}/preview-recipients`) {
      await route.fulfill({
        json: {
          recipients: [
            {
              person_id: "ffffffff-ffff-4fff-8fff-ffffffffffff",
              access_id: "99999999-9999-4999-8999-999999999999",
              name: "Alex",
              access_state: "onboarding",
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/events/${eventID}/preview`) {
      expect(request.method()).toBe("POST");
      expect(request.headers()["x-memento-csrf"]).toBe(csrfToken);
      await route.fulfill({
        json: {
          authorized: true,
          event_id: eventID,
          publication_id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
          title: "Family weekend",
          description: "",
          cover_media_id: items.first.id,
          media_count: 1,
          media: [{ ...items.first, available: true }],
          preview: true,
          capabilities: {
            comments: false,
            favorites: false,
            settings: false,
            downloads: false,
            record_engagement: false,
          },
        },
      });
      return;
    }
    const reviewMatch = path.match(
      /^\/api\/moments\/([^/]+)\/attendance-audience$/,
    );
    if (reviewMatch) {
      await route.fulfill({
        json: {
          target_kind: "moment",
          target_id: reviewMatch[1],
          version: 1,
          attendance_confirmed: false,
          audience_complete: false,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: true,
          proposal: [],
          approved_audience: null,
        },
      });
      return;
    }
    if (
      path === `/api/events/${eventID}/organization` &&
      request.method() === "PUT"
    ) {
      const body = request.postDataJSON() as OrganizeEventRequest;
      attempts.push(body);
      const outcome = outcomes[failureIndex];
      failureIndex += 1;
      if (outcome === "failed") {
        await route.fulfill({
          status: 503,
          json: { error: { message: "Autosave is temporarily unavailable." } },
        });
        return;
      }
      if (outcome === "conflict") {
        persisted = {
          ...persisted,
          version: persisted.version + 1,
          title: "Newer server work",
        };
        await route.fulfill({
          status: 409,
          json: {
            error: { message: "This Event changed in another browser." },
          },
        });
        return;
      }
      persisted = eventFromRequest(body, persisted);
      await route.fulfill({ json: persisted });
      return;
    }
    if (path === "/api/session/logout" && request.method() === "POST") {
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({
      status: 500,
      json: { error: { message: `Unexpected request: ${path}` } },
    });
  });

  return {
    attempts,
    persisted: () => persisted,
  };
}

async function openEvent(page: Page) {
  await page.goto("/?workspace=drafts");
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(
    page.getByRole("heading", { name: "Family weekend" }),
  ).toBeVisible();
}

test("@desktop @mobile publishes atomically and keeps Recipient preview read only", async ({
  page,
}) => {
  const ready = draft();
  ready.final_review_complete = true;
  ready.unassigned_media = [];
  ready.moments = ready.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  await mockCuratorAPI(page, [], ready);
  await openEvent(page);
  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
  }

  await page
    .getByLabel("Preview Recipient")
    .selectOption("ffffffff-ffff-4fff-8fff-ffffffffffff");
  await expect(
    page.getByText(/Pending Recipient: cannot access yet/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  const preview = page.getByRole("region", {
    name: "Read-only Recipient preview",
  });
  await expect(preview).toContainText("1 authorized Media items");
  await expect(preview).toContainText(
    "Preview activity is not recorded as Recipient engagement.",
  );
  for (const action of ["Comment", "Favorite", "Settings", "Download"]) {
    await expect(preview.getByRole("button", { name: action })).toBeDisabled();
  }

  await page.getByRole("button", { name: "Publish Event" }).click();
  await expect(
    page.getByText("Published revision 1 atomically."),
  ).toBeVisible();
  await expect(preview).not.toBeVisible();

  await page.reload();
  await expect(page.getByText(/Published · 2 Moments/)).toBeVisible();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
  }
  await expect(
    page.getByRole("button", { name: "Publish Event" }),
  ).toBeDisabled();
});

test("@desktop @mobile withdraws published access immediately and requires Publication to restore", async ({
  page,
}) => {
  const published = draft();
  published.lifecycle = "published";
  published.published_editable_version = published.version;
  published.final_review_complete = true;
  published.unassigned_media = [];
  published.moments = published.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  await mockCuratorAPI(page, [], published);
  page.on("dialog", (dialog) => dialog.accept());
  await openEvent(page);
  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
  }

  await page.getByLabel("Attributable reason").fill("Privacy request");
  await page.getByRole("button", { name: "Withdraw access" }).click();
  await expect(
    page.getByText(
      "Access withdrawn for 2 Recipients across 3 Media items. No external notification was sent.",
    ),
  ).toBeVisible();
  await expect(page.getByText(/Privacy request by Robin/)).toContainText(
    "Access remains withdrawn.",
  );
  await expect(
    page.getByRole("button", { name: "Publish Event" }),
  ).toBeDisabled();
});

test("@desktop organizes, orders, autosaves, and persists after reload", async ({
  page,
}) => {
  const server = await mockCuratorAPI(page);
  await openEvent(page);

  await expect(
    page.getByRole("navigation", { name: "Mobile workspace panes" }),
  ).toBeHidden();
  const workBox = await page.locator(".work-pane").boundingBox();
  const eventBox = await page.locator(".organize-pane").boundingBox();
  const inspectBox = await page.locator(".inspect-pane").boundingBox();
  expect(workBox).not.toBeNull();
  expect(eventBox).not.toBeNull();
  expect(inspectBox).not.toBeNull();
  expect(workBox!.x).toBeLessThan(eventBox!.x);
  expect(eventBox!.x).toBeLessThan(inspectBox!.x);

  await page
    .getByRole("button", { name: "Merge with previous Moment" })
    .nth(1)
    .click();
  await expect(page.locator(".moment-list .moment-card")).toHaveCount(1);

  await page.getByRole("checkbox", { name: /second photo/ }).check();
  await page
    .getByRole("button", { name: "Split selected into new Moment" })
    .click();
  await expect(page.locator(".moment-list .moment-card")).toHaveCount(2);
  const placementLabels = page.locator(".moment-list .moment-card > header p");
  await expect(placementLabels.nth(0)).toContainText("2026-05-01");
  await expect(placementLabels.nth(1)).toContainText("2026-05-01");

  const third = page.getByRole("checkbox", { name: /third photo/ });
  await third.focus();
  await expect(third).toBeFocused();
  await third.press("Alt+ArrowUp");

  await page.getByRole("checkbox", { name: /loose photo/ }).check();
  await page.getByLabel("Move selected to").selectOption({ label: "Friday" });
  await page.getByRole("button", { name: "Move selected Media" }).click();
  const covers = page.getByLabel("Cover");
  await covers.nth(0).selectOption(items.loose.id);
  await covers.nth(1).selectOption(items.second.id);
  await page.getByRole("button", { name: "Move Moment 2 earlier" }).click();

  await expect(page.getByText("All changes saved")).toBeVisible();
  await expect.poll(() => server.attempts.length).toBeGreaterThan(0);
  await expect
    .poll(() => server.persisted().moments[0].media_items[0].id)
    .toBe(items.second.id);
  expect(
    server.persisted().moments[1].media_items.map((item) => item.id),
  ).toEqual([items.third.id, items.first.id, items.loose.id]);

  await page.reload();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(page.locator(".moment-list .moment-card")).toHaveCount(2);
  await expect(page.getByLabel("Cover").nth(0)).toHaveValue(items.second.id);
  await expect(page.getByLabel("Cover").nth(1)).toHaveValue(items.loose.id);
});

test("@mobile drills down without clipping and manually populates a Moment", async ({
  page,
}) => {
  const server = await mockCuratorAPI(page);
  await page.goto("/?workspace=drafts");

  const paneNavigation = page.getByRole("navigation", {
    name: "Mobile workspace panes",
  });
  await expect(paneNavigation).toBeVisible();
  await expect(page.locator(".work-pane")).toBeVisible();
  await expect(page.locator(".organize-pane")).toBeHidden();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(
    page.getByRole("button", { name: "Event", pressed: true }),
  ).toBeVisible();
  await expect(page.locator(".work-pane")).toBeHidden();
  await expect(page.locator(".organize-pane")).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    )
    .toBe(true);

  await page.getByRole("checkbox", { name: /loose photo/ }).check();
  await page.getByLabel("New Moment day").fill("2026-05-03");
  await page
    .getByRole("button", { name: "Create Moment from selected Media" })
    .click();
  await expect(page.locator(".moment-list .moment-card")).toHaveCount(3);
  await expect(page.locator(".moment-list .moment-card").last()).toContainText(
    "2026-05-03",
  );

  await page
    .getByRole("button", { name: "Inspect Attendance and Audience" })
    .last()
    .click();
  await expect(
    page.getByRole("button", { name: "Inspect", pressed: true }),
  ).toBeVisible();
  await expect(page.locator(".organize-pane")).toBeHidden();
  await expect(page.locator(".inspect-pane")).toBeVisible();
  await expect(page.locator(".inspect-pane")).toBeFocused();
  await expect(
    page.getByRole("button", { name: "Confirm Attendance" }),
  ).toBeVisible();

  await expect(page.getByText("All changes saved")).toBeVisible();
  await expect.poll(() => server.persisted().moments.length).toBe(3);
});

test("@desktop recovers failed autosaves and stale conflicts", async ({
  page,
}) => {
  const server = await mockCuratorAPI(page, ["failed", "success", "conflict"]);
  await openEvent(page);

  const title = page.getByLabel("Title for Moment 1");
  await title.fill("Recovered title");
  await expect(page.getByRole("alert")).toContainText(
    "Autosave is temporarily unavailable.",
  );
  await title.fill("Latest recovered title");
  await page.getByRole("button", { name: "Retry autosave" }).click();
  await expect(page.getByText("All changes saved")).toBeVisible();
  expect(server.attempts[0].moments[0].title).toBe("Recovered title");
  expect(server.attempts[1].moments[0].title).toBe("Latest recovered title");

  await title.fill("My local conflict title");
  await expect(page.getByRole("alert")).toContainText(
    "Your edits have not overwritten the newer version.",
  );
  await page
    .getByRole("button", { name: "Replace newer version with my changes" })
    .click();
  await expect(page.getByText("All changes saved")).toBeVisible();
  expect(server.attempts[2].version).toBe(2);
  expect(server.attempts[3].version).toBe(3);
  expect(server.attempts[3].moments[0].title).toBe("My local conflict title");
});
