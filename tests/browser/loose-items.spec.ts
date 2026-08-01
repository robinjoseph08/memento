import { expect, test, type Page } from "@playwright/test";

import { problemResponse } from "../../app/test/problem";
import type {
  LooseItem,
  UpdateLooseItemRequest,
  WithdrawRequest,
} from "../../app/types/generated/events";

const csrf = "c".repeat(64);
const looseID = "11111111-1111-4111-8111-111111111111";
const pendingPersonID = "22222222-2222-4222-8222-222222222222";
const deniedPersonID = "33333333-3333-4333-8333-333333333333";

function looseItem(): LooseItem {
  return {
    id: looseID,
    lifecycle: "published",
    title: "Garden portrait",
    description: "Published description",
    grouping_timezone: "UTC",
    proposed_day: "2026-08-01",
    place_labels: ["Garden"],
    version: 1,
    audience_complete: true,
    published_editable_version: 1,
    has_staged_update: false,
    pending_withdrawal_publication: false,
    withdrawal_targets: [
      {
        target_kind: "loose_item",
        target_id: looseID,
        label: "Loose item: Garden portrait",
      },
    ],
    withdrawals: [],
    media_item: {
      id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      media_type: "image",
      width: 1200,
      height: 800,
      local_date_time: null,
    },
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  };
}

async function mockLooseWorkspace(
  page: Page,
  options: { sourceMissing?: boolean } = {},
) {
  let current = looseItem();
  if (options.sourceMissing)
    current = {
      ...current,
      lifecycle: "draft",
      published_editable_version: null,
      withdrawal_targets: [],
    };
  let reviewVersion = 1;
  const requests: Array<{
    path: string;
    method: string;
    body: unknown;
    csrf?: string;
  }> = [];
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const body: unknown = request.postData()
      ? (request.postDataJSON() as unknown)
      : undefined;
    requests.push({
      path,
      method,
      body,
      csrf: request.headers()["x-memento-csrf"],
    });
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
          session_type: "trusted",
          csrf_token: csrf,
          curator: true,
          onboarding_required: false,
        },
      });
      return;
    }
    if (path === "/api/session/refresh" && method === "POST") {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/api/loose-items" && method === "GET") {
      await route.fulfill({
        json: {
          loose_items: [
            {
              id: current.id,
              lifecycle: current.lifecycle,
              title: current.title,
              version: current.version,
              audience_complete: current.audience_complete,
              has_staged_update: current.has_staged_update,
              updated_at: current.updated_at,
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/loose-items/${looseID}` && method === "GET") {
      await route.fulfill({ json: current });
      return;
    }
    if (path === `/api/loose-items/${looseID}` && method === "PUT") {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      const update = body as UpdateLooseItemRequest;
      expect(update.version).toBe(current.version);
      current = {
        ...current,
        version: current.version + 1,
        title: update.title ?? "",
        description: update.description ?? "",
        grouping_timezone: update.grouping_timezone,
        proposed_day: update.proposed_day,
        place_labels: update.place_labels,
        has_staged_update: true,
      };
      await route.fulfill({ json: current });
      return;
    }
    if (
      path === `/api/loose-items/${looseID}/attendance-audience` &&
      method === "GET"
    ) {
      await route.fulfill({
        json: {
          target_kind: "loose_item",
          target_id: looseID,
          version: reviewVersion,
          attendance_confirmed: true,
          audience_complete: current.audience_complete,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: false,
          proposal: [],
          approved_audience: current.audience_complete
            ? {
                id: "audience-1",
                label: "Curator only",
                recipients: [],
                approved_at: "2026-08-01T00:00:00Z",
              }
            : null,
        },
      });
      return;
    }
    if (
      path === `/api/loose-items/${looseID}/audience/approve` &&
      method === "POST"
    ) {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      expect(request.headers()["if-match"]).toBe(String(reviewVersion));
      reviewVersion += 1;
      current = {
        ...current,
        version: current.version + 1,
        audience_complete: true,
        has_staged_update: true,
      };
      await route.fulfill({
        json: {
          version: reviewVersion,
          audience: {
            id: "audience-restored",
            label: "Curator only",
            recipients: [],
            approved_at: "2026-08-01T01:00:00Z",
          },
        },
      });
      return;
    }
    if (path === `/api/loose-items/${looseID}/preview-recipients`) {
      await route.fulfill({
        json: {
          recipients: [
            {
              person_id: pendingPersonID,
              access_id: "pending-access",
              name: "Alex",
              access_state: "onboarding",
            },
            {
              person_id: deniedPersonID,
              access_id: "denied-access",
              name: "Bailey",
              access_state: "completed",
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/loose-items/${looseID}/preview` && method === "POST") {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      const personID = new URL(request.url()).searchParams.get(
        "recipient_person_id",
      );
      if (personID === deniedPersonID) {
        await route.fulfill({
          json: {
            authorized: false,
            loose_item_id: "",
            publication_id: "",
            title: "",
            description: "",
            place_labels: [],
            proposed_day: null,
            media: {
              id: "",
              media_type: "",
              width: null,
              height: null,
              local_date_time: null,
              available: false,
            },
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
      await route.fulfill({
        json: {
          authorized: true,
          loose_item_id: looseID,
          publication_id: "publication-1",
          title: current.title,
          description: current.description,
          place_labels: current.place_labels,
          proposed_day: current.proposed_day,
          media: { ...current.media_item, available: true },
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
    if (path === "/api/withdrawals" && method === "POST") {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      const withdrawal = body as WithdrawRequest;
      expect(withdrawal).toEqual({
        target_kind: "loose_item",
        target_id: looseID,
        reason: "Privacy correction",
      });
      current = {
        ...current,
        version: current.version + 1,
        audience_complete: false,
        has_staged_update: true,
        pending_withdrawal_publication: true,
        withdrawal_targets: [],
        withdrawals: [
          {
            id: "withdrawal-1",
            target_kind: "loose_item",
            target_id: looseID,
            reason: "Privacy correction",
            withdrawn_by_name: "Robin",
            withdrawn_at: "2026-08-01T02:00:00Z",
            restored_by_publication_id: null,
            restored_at: null,
            affected_recipient_count: 1,
            affected_media_count: 1,
            affected_event_count: 0,
          },
        ],
      };
      await route.fulfill({ status: 201, json: current.withdrawals[0] });
      return;
    }
    if (
      path === `/api/loose-items/${looseID}/publications` &&
      method === "POST"
    ) {
      expect(request.headers()["x-memento-csrf"]).toBe(csrf);
      expect(body).toMatchObject({ version: current.version });
      if (options.sourceMissing) {
        await route.fulfill({
          status: 409,
          json: problemResponse(
            "The Loose item's Source Media is unavailable. Relink it before publishing.",
            409,
          ),
        });
        return;
      }
      const restoring = current.pending_withdrawal_publication;
      current = {
        ...current,
        published_editable_version: current.version,
        has_staged_update: false,
        pending_withdrawal_publication: false,
        withdrawal_targets: [
          {
            target_kind: "loose_item",
            target_id: looseID,
            label: "Loose item: Garden portrait",
          },
        ],
        withdrawals: current.withdrawals.map((withdrawal) =>
          withdrawal.restored_at || !restoring
            ? withdrawal
            : {
                ...withdrawal,
                restored_by_publication_id: "publication-restored",
                restored_at: "2026-08-01T03:00:00Z",
              },
        ),
      };
      await route.fulfill({
        status: 201,
        json: {
          id: restoring ? "publication-restored" : "publication-correction",
          loose_item_id: looseID,
          revision: restoring ? 3 : 2,
          editable_version: current.version,
          notify_recipients: true,
          committed_at: "2026-08-01T03:00:00Z",
        },
      });
      return;
    }
    await route.fulfill({
      status: 500,
      json: problemResponse(`Unexpected request: ${method} ${path}`, 500),
    });
  });
  return { requests, current: () => current };
}

async function openReviewPane(page: Page) {
  if ((page.viewportSize()?.width ?? 1280) <= 1024)
    await page.getByRole("button", { name: "Review", exact: true }).click();
}

async function openLooseDetailsPane(page: Page) {
  if ((page.viewportSize()?.width ?? 1280) <= 1024)
    await page.getByRole("button", { name: "Loose item", exact: true }).click();
}

test("@desktop @mobile shows the mocked Loose correction, Pending preview, Withdrawal, and restoration workflow", async ({
  page,
}) => {
  const server = await mockLooseWorkspace(page);
  await page.goto(`/?workspace=drafts&loose=${looseID}`);
  await expect(
    page.getByRole("heading", { name: "Garden portrait", exact: true }),
  ).toBeVisible();
  await openReviewPane(page);

  await page.getByLabel("Preview Recipient").selectOption(pendingPersonID);
  await expect(
    page.getByText(/Pending Recipient: cannot access yet/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  const preview = page.getByRole("region", {
    name: "Read-only Recipient preview",
  });
  await expect(preview).toContainText("1 authorized Media item");
  for (const action of ["Comment", "Favorite", "Settings", "Download"])
    await expect(preview.getByRole("button", { name: action })).toBeDisabled();
  expect(
    server.requests.filter(({ path }) =>
      ["/comments", "/favorites", "/archives", "/engagement", "/original"].some(
        (forbidden) => path.includes(forbidden),
      ),
    ),
  ).toEqual([]);

  await openLooseDetailsPane(page);
  await page.getByLabel("Description").fill("Private corrected description");
  await expect(
    page.getByText(
      /This correction remains private. Recipients continue to see the current Publication/,
    ),
  ).toBeVisible();
  await openReviewPane(page);
  await expect(preview).toHaveCount(0);
  await page
    .getByRole("button", { name: "Publish Loose item correction" })
    .click();
  await expect(
    page.getByText("Published Loose item revision 2."),
  ).toBeVisible();

  page.once("dialog", async (dialog) => dialog.accept());
  await page.getByLabel("Attributable reason").fill("Privacy correction");
  await page
    .getByRole("button", { name: "Withdraw Loose item access" })
    .click();
  await expect(
    page.getByText(/Access withdrawn immediately for 1 Recipients/),
  ).toBeVisible();
  await expect(page.getByText(/Access remains withdrawn/)).toBeVisible();
  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await openLooseDetailsPane(page);
    await expect(
      page.getByText("Next action: Fresh Audience review"),
    ).toBeVisible();
    await openReviewPane(page);
  } else {
    await expect(
      page.getByText("Next action: Fresh Audience review"),
    ).toBeVisible();
  }
  await expect(
    page.getByRole("button", { name: "Preview as Recipient" }),
  ).toBeDisabled();

  await page.getByRole("button", { name: "Approve Curator only" }).click();
  await expect(
    page.getByRole("button", { name: "Publish Loose item restoration" }),
  ).toBeEnabled();

  await page.getByLabel("Preview Recipient").selectOption(deniedPersonID);
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  await expect(
    page.getByText("Nothing is shared with this Recipient."),
  ).toBeVisible();
  await expect(preview).not.toContainText("authorized Media item");

  await page
    .getByRole("button", { name: "Publish Loose item restoration" })
    .click();
  await expect(
    page.getByText("Published Loose item revision 3."),
  ).toBeVisible();
  await expect(page.getByText(/Restored by a later Publication/)).toBeVisible();
  expect(server.current().pending_withdrawal_publication).toBe(false);
  expect(
    server.requests.filter(
      ({ path, method }) =>
        path === `/api/loose-items/${looseID}/publications` &&
        method === "POST",
    ),
  ).toHaveLength(2);
});

test("@desktop @mobile denies Publication while Source Media is missing", async ({
  page,
}) => {
  await mockLooseWorkspace(page, { sourceMissing: true });
  await page.goto(`/?workspace=drafts&loose=${looseID}`);
  await openReviewPane(page);
  await page.getByRole("button", { name: "Publish Loose item" }).click();
  await expect(page.getByText(/Source Media is unavailable/)).toBeVisible();
  await expect(page.getByText(/Published Loose item revision/)).toHaveCount(0);
});
