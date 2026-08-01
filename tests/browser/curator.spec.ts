import { expect, test, type Page } from "@playwright/test";

import { problemResponse } from "../../app/test/problem";
import type {
  Event as DraftEvent,
  MediaItem,
  OrganizeEventRequest,
  WithdrawRequest,
} from "../../app/types/generated/events";

const csrfToken = "c".repeat(64);
const eventID = "11111111-1111-4111-8111-111111111111";
const momentOneID = "22222222-2222-4222-8222-222222222222";
const momentTwoID = "33333333-3333-4333-8333-333333333333";
const deletedMomentID = "44444444-4444-4444-8444-444444444444";

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
  removed: media("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "removed photo"),
};

function draft(version = 1): DraftEvent {
  return {
    id: eventID,
    lifecycle: "draft",
    title: "Family weekend",
    description: "",
    place_labels: [],
    grouping_timezone: "UTC",
    version,
    final_review_complete: false,
    published_editable_version: null,
    published_attendance_recovery_required: false,
    pending_withdrawal_publication: false,
    staged_update: null,
    sources: [],
    moments: [
      {
        id: momentOneID,
        title: "Friday",
        place_labels: [],
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
        place_labels: [],
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
    withdrawal_targets: [
      {
        target_kind: "event",
        target_id: eventID,
        label: "Event: Family weekend",
      },
      {
        target_kind: "moment",
        target_id: momentOneID,
        label: "Moment: Friday",
      },
      {
        target_kind: "moment",
        target_id: momentTwoID,
        label: "Moment: Saturday",
      },
      {
        target_kind: "media",
        target_id: items.first.id,
        label: "Media: first photo",
      },
      {
        target_kind: "media",
        target_id: items.second.id,
        label: "Media: second photo",
      },
      {
        target_kind: "media",
        target_id: items.third.id,
        label: "Media: third photo",
      },
    ],
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
  next.place_labels = request.place_labels;
  next.moments = request.moments.map((moment) => ({
    id: moment.id,
    title: moment.title ?? "",
    place_labels: moment.place_labels,
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
  options: { failEventReloadAfterWithdrawal?: boolean } = {},
) {
  let persisted = initial;
  const attempts: OrganizeEventRequest[] = [];
  const withdrawalRequests: WithdrawRequest[] = [];
  let failureIndex = 0;
  let withdrawalCommitted = false;
  let previewRequests = 0;

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: problemResponse("Setup not found.", 404),
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
              has_staged_update: persisted.staged_update !== null,
              updated_at: persisted.updated_at,
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/events/${eventID}` && request.method() === "GET") {
      if (withdrawalCommitted && options.failEventReloadAfterWithdrawal) {
        await route.fulfill({
          status: 503,
          json: problemResponse("The Event reload failed.", 503),
        });
        return;
      }
      await route.fulfill({ json: persisted });
      return;
    }
    if (path === "/api/withdrawals" && request.method() === "POST") {
      expect(request.headers()["x-memento-csrf"]).toBe(csrfToken);
      const body = request.postDataJSON() as WithdrawRequest;
      withdrawalRequests.push(body);
      const affected =
        body.target_kind === "event"
          ? { recipients: 2, media: 3 }
          : { recipients: 1, media: 1 };
      persisted = {
        ...persisted,
        version: persisted.version + 1,
        final_review_complete: false,
        moments: persisted.moments.map((moment) => ({
          ...moment,
          audience_complete: false,
        })),
        withdrawal_targets: persisted.withdrawal_targets.filter(
          (target) =>
            target.target_kind !== body.target_kind ||
            target.target_id !== body.target_id,
        ),
        withdrawals: [
          ...persisted.withdrawals,
          {
            id: `${persisted.withdrawals.length + 7}7777777-7777-4777-8777-777777777777`,
            target_kind: body.target_kind,
            target_id: body.target_id,
            reason: body.reason,
            withdrawn_by_name: "Robin",
            withdrawn_at: "2026-05-03T00:00:00Z",
            restored_by_publication_id: null,
            restored_at: null,
            affected_recipient_count: affected.recipients,
            affected_media_count: affected.media,
            affected_event_count: 1,
          },
        ],
      };
      withdrawalCommitted = true;
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
        staged_update: null,
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
      previewRequests += 1;
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
          json: problemResponse("Autosave is temporarily unavailable.", 503),
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
          json: problemResponse("This Event changed in another browser.", 409),
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
      json: problemResponse(`Unexpected request: ${path}`, 500),
    });
  });

  return {
    attempts,
    withdrawalRequests,
    persisted: () => persisted,
    previewRequests: () => previewRequests,
  };
}

async function openEvent(page: Page) {
  await page.goto("/?workspace=drafts");
  await page.getByRole("button", { name: /family weekend/i }).click();
  await expect(
    page.getByRole("heading", { name: /family weekend/i }),
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
  await expect(
    page.getByRole("heading", { name: /Family weekend/i }),
  ).toBeVisible();
  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
  } else {
    await expect(page.getByText(/Published · 2 Moments/)).toBeVisible();
  }
  await expect(
    page.getByRole("button", { name: "Publish Event" }),
  ).toBeDisabled();
});

for (const target of [
  { name: "Event", id: eventID, recipients: 2, media: 3 },
  { name: "Moment", id: momentOneID, recipients: 1, media: 1 },
  { name: "Media", id: items.first.id, recipients: 1, media: 1 },
]) {
  test(`@desktop @mobile withdraws published access for ${target.name} with persistent confirmation`, async ({
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
    const server = await mockCuratorAPI(page, [], published);
    let confirmation = "";
    page.once("dialog", async (dialog) => {
      confirmation = dialog.message();
      await dialog.accept();
    });
    await openEvent(page);
    if ((page.viewportSize()?.width ?? 1280) <= 1024) {
      await page.getByRole("button", { name: "Inspect", exact: true }).click();
    }

    await page.getByLabel("Currently published target").selectOption(target.id);
    await page.getByLabel("Attributable reason").fill("Privacy request");
    await page.getByRole("button", { name: "Withdraw access" }).click();
    await expect(
      page.getByText(
        `Access withdrawn for ${target.recipients} Recipients across ${target.media} Media items. Withdrawal created no new external notification. A delivery already handed off before it committed may still arrive.`,
      ),
    ).toBeVisible();
    expect(confirmation).toBe(
      "Withdraw Recipient access immediately? Identity and history will be preserved.",
    );
    expect(server.withdrawalRequests).toEqual([
      {
        target_kind: target.name.toLowerCase(),
        target_id: target.id,
        reason: "Privacy request",
      },
    ]);
    await expect(page.getByText(/Privacy request by Robin/)).toContainText(
      "Access remains withdrawn.",
    );
    await expect(
      page.getByRole("button", { name: "Publish Event" }),
    ).toBeDisabled();

    await page.reload();
    await expect(
      page.getByRole("heading", { name: /Family weekend/i }),
    ).toBeVisible();
    if ((page.viewportSize()?.width ?? 1280) <= 1024) {
      await page.getByRole("button", { name: "Inspect", exact: true }).click();
    }
    await expect(page.getByText(/Privacy request by Robin/)).toContainText(
      "Access remains withdrawn.",
    );
    await expect(
      page.getByRole("button", { name: "Publish Event" }),
    ).toBeDisabled();
  });
}

test("@desktop @mobile fails closed when Withdrawal authority cannot reload", async ({
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
  const server = await mockCuratorAPI(page, [], published, {
    failEventReloadAfterWithdrawal: true,
  });
  page.once("dialog", async (dialog) => dialog.accept());
  await openEvent(page);
  if ((page.viewportSize()?.width ?? 1280) <= 1024)
    await page.getByRole("button", { name: "Inspect", exact: true }).click();

  await page
    .getByLabel("Preview Recipient")
    .selectOption("ffffffff-ffff-4fff-8fff-ffffffffffff");
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  await expect(page.getByText("1 authorized Media items")).toBeVisible();
  expect(server.previewRequests()).toBe(1);

  await page.getByLabel("Attributable reason").fill("Privacy request");
  await page.getByRole("button", { name: "Withdraw access" }).click();

  await expect(
    page.getByText(
      "Reload the authoritative Event before Preview, Withdrawal, or Publication can continue.",
    ),
  ).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Read-only Recipient preview" }),
  ).toHaveCount(0);
  await expect(page.getByText("1 authorized Media items")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Preview as Recipient" }),
  ).toBeDisabled();
  await expect.poll(server.previewRequests).toBe(1);
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
  await page
    .getByRole("button", { name: "Move selected Media", exact: true })
    .click();
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
  await expect(
    page.getByRole("heading", { name: /Family weekend/i }),
  ).toBeVisible();
  await expect(page.locator(".moment-list .moment-card")).toHaveCount(2);
  await expect(page.getByLabel("Cover").nth(0)).toHaveValue(items.second.id);
  await expect(page.getByLabel("Cover").nth(1)).toHaveValue(items.loose.id);
});

test("@mobile Staged review fits every change category in the page", async ({
  page,
}) => {
  const staged = draft();
  staged.lifecycle = "published";
  staged.title = "Corrected family weekend";
  staged.description = "The complete corrected description";
  staged.place_labels = ["Coastal overlook", "Garden terrace"];
  staged.grouping_timezone = "America/New_York";
  staged.moments[0].place_labels = ["Breakfast room", "Harbor view"];
  staged.published_editable_version = staged.version;
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "addition",
        count: 1,
        media_item_ids: [items.first.id],
        moment_ids: [],
        detail: "Media added",
      },
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.removed.id],
        moment_ids: [],
        removed_media: [
          {
            id: items.removed.id,
            media_type: items.removed.media_type,
            local_date_time: items.removed.local_date_time,
            restorable: false,
          },
        ],
        detail: "Media removed",
      },
      {
        kind: "move",
        count: 1,
        media_item_ids: [items.second.id],
        moment_ids: [],
        detail: "Media moved or reordered",
      },
      {
        kind: "metadata",
        count: 4,
        media_item_ids: [items.third.id],
        moment_ids: [momentOneID],
        event_metadata_fields: [
          "title",
          "description",
          "place_labels",
          "grouping_timezone",
        ],
        detail: "Event, Moment, or Media metadata edited",
      },
      {
        kind: "moment_structure",
        count: 1,
        media_item_ids: [],
        moment_ids: [momentTwoID, deletedMomentID],
        deleted_moments: [
          {
            id: deletedMomentID,
            title: "Sunday breakfast",
            proposed_day: "2026-05-03",
          },
        ],
        detail: "Moment structure or ordering changed",
      },
      {
        kind: "access",
        count: 1,
        media_item_ids: [items.second.id],
        moment_ids: [momentOneID, momentTwoID],
        recipient_access: [
          {
            recipient_person_id: "55555555-5555-4555-8555-555555555555",
            recipient_name: "Alex",
            granted_media_count: 2,
            revoked_media_count: 1,
          },
        ],
        detail: "Global Recipient Media access granted or revoked",
      },
    ],
  };
  await mockCuratorAPI(page, [], staged);
  await openEvent(page);

  const review = page.getByRole("region", { name: "Staged update review" });
  await expect(review).toBeVisible();
  const eventMetadata = review.locator(".staged-event-metadata");
  await expect(eventMetadata).toContainText("Coastal overlook, Garden terrace");
  await expect(eventMetadata.locator(".staged-metadata")).toHaveCount(4);
  await expect(eventMetadata.getByText("Staged: Metadata edits")).toHaveCount(
    4,
  );
  await expect(page.getByLabel("Place labels for Moment 1")).toHaveValue(
    "Breakfast room, Harbor view",
  );
  await expect(
    page.locator(".moment-card").filter({
      has: page.getByLabel("Place labels for Moment 1"),
    }),
  ).toHaveClass(/staged-metadata/);
  const summary = review.getByRole("list", { name: "Net change summary" });
  await expect(summary.locator(":scope > li")).toHaveCount(6);
  for (const detail of [
    "Media added",
    "Media removed",
    "Media moved or reordered",
    "Event, Moment, or Media metadata edited",
    "Moment structure or ordering changed",
    "Global Recipient Media access granted or revoked",
  ]) {
    await expect(summary).toContainText(detail);
  }
  await expect(review).toContainText("Removed Media");
  await expect(review).toContainText("Sunday breakfast");
  await expect(review).toContainText("Alex");
  await expect(review).toContainText("2 Media granted");
  await expect(review).toContainText("1 Media revoked");
  await expect(
    page.getByLabel("Staged changes: Moves and ordering, Access changes"),
  ).toBeVisible();
  await expect(
    page.getByLabel("Staged changes: Metadata edits, Access changes"),
  ).toBeVisible();
  await expect(
    page.getByLabel("Staged changes: Moment structure, Access changes"),
  ).toBeVisible();
  await expect
    .poll(() =>
      review.evaluate((element) => element.scrollWidth <= element.clientWidth),
    )
    .toBe(true);
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    )
    .toBe(true);
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
    page.getByRole("button", { name: "Event", pressed: true, exact: true }),
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
