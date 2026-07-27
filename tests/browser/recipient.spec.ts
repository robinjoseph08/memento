import { expect, test, type Page } from "@playwright/test";

const media = {
  id: "11111111-1111-4111-8111-111111111111",
  media_type: "image",
  width: 1600,
  height: 900,
  local_date_time: "2026-07-27T12:00:00Z",
  available: true,
  thumbnail_url: "/api/me/media/11111111-1111-4111-8111-111111111111/thumbnail",
};

const secondMedia = {
  ...media,
  id: "44444444-4444-4444-8444-444444444444",
  media_type: "video",
  width: 900,
  height: 1600,
  local_date_time: "2026-07-27T13:00:00Z",
  thumbnail_url: "/api/me/media/44444444-4444-4444-8444-444444444444/thumbnail",
};

const event = {
  id: "22222222-2222-4222-8222-222222222222",
  publication_id: "33333333-3333-4333-8333-333333333333",
  title: "Family weekend",
  description: "One seamless authorized Event",
  committed_at: "2026-07-27T12:00:00Z",
  cover_media_id: media.id,
  cover_available: true,
  thumbnail_url: media.thumbnail_url,
  media_count: 1,
};

async function recipientAPI(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
    } else if (path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Alex",
          session_type: "trusted",
          csrf_token: "c".repeat(64),
          curator: false,
          onboarding_required: false,
        },
      });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204, body: "" });
    } else if (path === "/api/me/photos") {
      const cursor = url.searchParams.get("cursor");
      if (cursor === null) {
        await route.fulfill({
          json: { media: [media], next_cursor: "photos-next" },
        });
      } else if (cursor === "photos-next") {
        await route.fulfill({
          json: { media: [secondMedia], next_cursor: null },
        });
      } else {
        await route.fulfill({
          status: 400,
          json: { error: { message: "invalid cursor" } },
        });
      }
    } else if (path === "/api/me/new-for-you") {
      await route.fulfill({ json: { events: [event] } });
    } else if (path === "/api/me/events" && request.method() === "GET") {
      await route.fulfill({ json: { events: [event], next_cursor: null } });
    } else if (path === `/api/me/events/${event.id}`) {
      await route.fulfill({
        json: { ...event, media: [media], next_cursor: null },
      });
    } else if (
      path === media.thumbnail_url ||
      path === secondMedia.thumbnail_url
    ) {
      await route.fulfill({
        contentType: "image/png",
        body: Buffer.from(
          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
          "base64",
        ),
      });
    } else if (path.endsWith("/seen")) {
      await route.fulfill({ status: 204, body: "" });
    } else if (path === "/api/sessions") {
      await route.fulfill({ json: { sessions: [] } });
    } else if (path === "/api/me/invitation-suggestions") {
      await route.fulfill({ json: { suggestions: [] } });
    } else if (path === "/api/me/interest-list") {
      await route.fulfill({
        json: {
          recipient: {
            id: "recipient",
            display_name: "Alex",
            sort_name: "alex",
          },
          version: 0,
          entries: [],
          history: [],
        },
      });
    } else if (path === "/api/me/people") {
      await route.fulfill({ json: { people: [] } });
    } else {
      await route.fulfill({
        status: 404,
        json: { error: { message: "not found" } },
      });
    }
  });
}

test("@desktop Recipient lands on Photos and sees only filtered Event totals", async ({
  page,
}) => {
  await recipientAPI(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "New for you" }),
  ).toBeVisible();
  await expect(page.locator(".library-rail")).toBeVisible();
  await expect(page.locator(".mobile-library-nav")).toBeHidden();
  await expect(page.getByText("hidden", { exact: false })).toHaveCount(0);

  await page.getByRole("button", { name: "Load more photos" }).click();
  await expect(page.getByAltText("Authorized video")).toBeVisible();
  await expect(page.getByAltText("Authorized photo")).toBeVisible();

  await page
    .locator(".library-rail")
    .getByRole("button", { name: "Events" })
    .click();
  await expect(page.getByText("1 item")).toBeVisible();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(page.getByText("1 items")).toBeVisible();
  await expect(page.getByText(/Moment/)).toHaveCount(0);
});

test("@mobile complete thumbnails and compact navigation do not expose inaccessible totals", async ({
  page,
}) => {
  await recipientAPI(page);
  await page.goto("/");

  const thumbnail = page.getByAltText("Authorized photo");
  await expect(thumbnail).toBeVisible();
  await expect(thumbnail).toHaveCSS("object-fit", "contain");
  await expect(page.locator(".mobile-library-nav")).toBeVisible();
  await expect(page.locator(".library-rail")).toBeHidden();
  await expect(page.getByLabel("Jump to date")).toBeVisible();
  await expect(page.getByText(/total/i)).toHaveCount(0);

  await page
    .locator(".mobile-library-nav")
    .getByRole("button", { name: "Favorites" })
    .click();
  await expect(
    page.getByText("Favorites aren't shared with other recipients."),
  ).toBeVisible();
});
