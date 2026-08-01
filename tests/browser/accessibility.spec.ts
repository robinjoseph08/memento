import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page, type Request } from "@playwright/test";

const csrfToken = "c".repeat(64);
const eventID = "11111111-1111-4111-8111-111111111111";
const momentID = "22222222-2222-4222-8222-222222222222";
const mediaID = "33333333-3333-4333-8333-333333333333";
const recipientID = "44444444-4444-4444-8444-444444444444";
const inaccessibleMediaID = "99999999-9999-4999-8999-999999999999";
const transparentPixel = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  "base64",
);

async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(results.violations).toEqual([]);
}

function pathOf(request: Request) {
  return new URL(request.url()).pathname;
}

async function mockOnboarding(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = pathOf(request);
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
          csrf_token: csrfToken,
          curator: false,
          onboarding_required: true,
        },
      });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204 });
    } else if (path === "/api/onboarding") {
      await route.fulfill({
        json: {
          recipient_name: "Alex",
          privacy_acknowledged: false,
          engagement_acknowledged: false,
          interest_list_acknowledged: false,
          email_previews_acknowledged: false,
          push_guidance_acknowledged: false,
          email_preference: "immediate",
          session_type: "trusted",
        },
      });
    } else if (path === "/api/me/interest-list") {
      await route.fulfill({
        json: {
          recipient: {
            id: recipientID,
            display_name: "Alex",
            sort_name: "alex",
          },
          version: 0,
          entries: [],
          history: [],
        },
      });
    } else if (path === "/api/me/people/search") {
      await route.fulfill({ json: { people: [], next_cursor: null } });
    } else {
      await route.fulfill({ status: 404, json: { error: { message: path } } });
    }
  });
}

const inaccessibleMedia = {
  id: inaccessibleMediaID,
  media_type: "image",
  width: 1200,
  height: 800,
  local_date_time: "2026-07-27T13:00:00Z",
  capture_date: "2026-07-27",
  available: true,
  thumbnail_url: `/api/me/media/${inaccessibleMediaID}/thumbnail`,
  preview_url: `/api/me/media/${inaccessibleMediaID}/preview`,
  video_url: "",
  original_url: `/api/me/media/${inaccessibleMediaID}/original`,
  private_label: "Hidden family photo",
};

const media = {
  id: mediaID,
  media_type: "image",
  width: 1600,
  height: 900,
  local_date_time: "2026-07-27T12:00:00Z",
  capture_date: "2026-07-27",
  available: true,
  thumbnail_url: `/api/me/media/${mediaID}/thumbnail`,
  preview_url: `/api/me/media/${mediaID}/preview`,
  video_url: "",
  original_url: `/api/me/media/${mediaID}/original`,
};

const olderMedia = {
  ...media,
  id: "77777777-7777-4777-8777-777777777777",
  local_date_time: "2026-06-15T12:00:00Z",
  capture_date: "2026-06-15",
  thumbnail_url: "/api/me/media/77777777-7777-4777-8777-777777777777/thumbnail",
  preview_url: "/api/me/media/77777777-7777-4777-8777-777777777777/preview",
  original_url: "/api/me/media/77777777-7777-4777-8777-777777777777/original",
};

const serverMedia = [media, olderMedia, inaccessibleMedia];
const authorizedMediaIDs = new Set([media.id, olderMedia.id]);

const publishedEvent = {
  id: eventID,
  publication_id: "55555555-5555-4555-8555-555555555555",
  title: "Family weekend",
  description: "One seamless authorized Event",
  committed_at: "2026-07-27T12:00:00Z",
  cover_media_id: mediaID,
  cover_available: true,
  thumbnail_url: media.thumbnail_url,
  media_count: 1,
};

async function mockRecipient(page: Page) {
  let favorite = false;
  const comments: Array<{
    id: string;
    media_item_id: string;
    author_name: string;
    body: string;
    state: string;
    version: number;
    created_at: string;
    edited_at: null;
    can_edit: boolean;
    can_delete: boolean;
    can_moderate: boolean;
    moderator_name: null;
  }> = [];
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
          csrf_token: csrfToken,
          curator: false,
          onboarding_required: false,
        },
      });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204 });
    } else if (
      path === "/api/me/photos/chronology" ||
      path === "/api/me/favorites/chronology"
    ) {
      await route.fulfill({
        json: {
          dates: [
            { capture_date: "2026-07-27", media_count: 1, cursor: "" },
            { capture_date: "2026-06-15", media_count: 1, cursor: "older" },
          ],
        },
      });
    } else if (path === "/api/me/photos") {
      const authorizedMedia = serverMedia.filter((item) =>
        authorizedMediaIDs.has(item.id),
      );
      await route.fulfill({
        json: {
          media:
            url.searchParams.get("cursor") === "older"
              ? [olderMedia]
              : authorizedMedia,
          next_cursor: null,
        },
      });
    } else if (path === "/api/me/favorites") {
      await route.fulfill({ json: { media: [media], next_cursor: null } });
    } else if (path === "/api/me/new-for-you") {
      await route.fulfill({ json: { events: [publishedEvent] } });
    } else if (path === "/api/me/events" && request.method() === "GET") {
      await route.fulfill({
        json: { events: [publishedEvent], next_cursor: null },
      });
    } else if (path === `/api/me/events/${eventID}`) {
      await route.fulfill({
        json: {
          ...publishedEvent,
          media: serverMedia.filter((item) => item.id === mediaID),
          next_cursor: null,
        },
      });
    } else if (
      path === media.thumbnail_url ||
      path === media.preview_url ||
      path === olderMedia.thumbnail_url ||
      path === olderMedia.preview_url
    ) {
      await route.fulfill({ contentType: "image/png", body: transparentPixel });
    } else if (path === `/api/favorites/${mediaID}`) {
      if (request.method() === "PUT") favorite = true;
      if (request.method() === "DELETE") favorite = false;
      await route.fulfill({ json: { media_item_id: mediaID, favorite } });
    } else if (path === `/api/comments/media/${mediaID}`) {
      if (request.method() === "POST") {
        const body = (request.postDataJSON() as { body: string }).body;
        const comment = {
          id: "88888888-8888-4888-8888-888888888888",
          media_item_id: mediaID,
          author_name: "Alex",
          body,
          state: "active",
          version: 1,
          created_at: "2026-07-27T14:00:00Z",
          edited_at: null,
          can_edit: true,
          can_delete: true,
          can_moderate: false,
          moderator_name: null,
        };
        comments.splice(0, comments.length, comment);
        await route.fulfill({ status: 201, json: comment });
      } else {
        await route.fulfill({
          json: {
            comments,
            can_mute: comments.length > 0,
            muted: false,
            next_cursor: null,
          },
        });
      }
    } else if (path === "/api/search" && request.method() === "POST") {
      await route.fulfill({
        json: {
          photos: [media],
          events: [],
          people: [],
          total_photos: 1,
          total_events: 0,
          has_more: false,
        },
      });
    } else if (path === "/api/me/archives" && request.method() === "POST") {
      await route.fulfill({
        status: 201,
        json: {
          name: "Family-weekend",
          item_count: 1,
          total_size: 3,
          expires_at: "2099-01-01T00:00:00Z",
          parts: [
            {
              part_number: 1,
              size: 3,
              filename: "Family-weekend.zip",
              download_url: "/api/me/archives/parts/1?token=private-token",
            },
          ],
        },
      });
    } else if (path === "/api/sessions") {
      await route.fulfill({ json: { sessions: [] } });
    } else if (
      path === "/api/me/invitation-suggestions" ||
      path === "/api/invitation-suggestions"
    ) {
      await route.fulfill({ json: { suggestions: [] } });
    } else if (path === "/api/me/interest-list") {
      await route.fulfill({
        json: {
          recipient: {
            id: recipientID,
            display_name: "Alex",
            sort_name: "alex",
          },
          version: 0,
          entries: [],
          history: [],
        },
      });
    } else if (path === "/api/me/people/search") {
      await route.fulfill({ json: { people: [], next_cursor: null } });
    } else if (path === "/api/notification-preferences") {
      await route.fulfill({
        json: {
          email_preference: "immediate",
          weekly_day: 0,
          weekly_time: "09:00",
          weekly_timezone: "UTC",
        },
      });
    } else if (path === "/api/push") {
      await route.fulfill({ json: { available: false, enrolled: false } });
    } else {
      await route.fulfill({ status: 404, json: { error: { message: path } } });
    }
  });
}

const draftEvent = {
  id: eventID,
  lifecycle: "draft",
  title: "Family weekend",
  description: "Ready to publish",
  place_labels: ["Family home"],
  grouping_timezone: "UTC",
  version: 1,
  final_review_complete: true,
  published_editable_version: null,
  published_attendance_recovery_required: false,
  pending_withdrawal_publication: false,
  staged_update: null,
  sources: [],
  moments: [
    {
      id: momentID,
      title: "Saturday",
      place_labels: ["Family home"],
      proposed_day: "2026-07-27",
      grouping_timezone: "UTC",
      source_days: ["2026-07-27"],
      proposal_kind: "local_day",
      cover_media_item_id: mediaID,
      attendance_complete: true,
      audience_complete: true,
      media_items: [
        {
          id: mediaID,
          media_type: "photo",
          width: 1600,
          height: 900,
          local_date_time: "2026-07-27T12:00:00Z",
        },
      ],
    },
  ],
  unassigned_media: [],
  withdrawal_targets: [],
  withdrawals: [],
  created_at: "2026-07-27T12:00:00Z",
  updated_at: "2026-07-27T12:00:00Z",
};

async function mockPublication(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = pathOf(request);
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
    } else if (path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Robin",
          session_type: "trusted",
          csrf_token: csrfToken,
          curator: true,
        },
      });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204 });
    } else if (path === "/api/events") {
      await route.fulfill({
        json: {
          events: [
            {
              id: eventID,
              lifecycle: "draft",
              title: "Family weekend",
              version: 1,
              moment_count: 1,
              unassigned_count: 0,
              has_staged_update: false,
              updated_at: "2026-07-27T12:00:00Z",
            },
          ],
        },
      });
    } else if (path === `/api/events/${eventID}`) {
      await route.fulfill({ json: draftEvent });
    } else if (path === `/api/moments/${momentID}/attendance-audience`) {
      await route.fulfill({
        json: {
          target_kind: "moment",
          target_id: momentID,
          version: 1,
          attendance_confirmed: true,
          audience_complete: true,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: true,
          proposal: [],
          approved_audience: { recipients: [] },
        },
      });
    } else if (path === `/api/events/${eventID}/preview-recipients`) {
      await route.fulfill({
        json: {
          recipients: [
            {
              person_id: recipientID,
              access_id: "66666666-6666-4666-8666-666666666666",
              name: "Alex",
              access_state: "completed",
            },
          ],
        },
      });
    } else {
      await route.fulfill({ status: 404, json: { error: { message: path } } });
    }
  });
}

async function expectNoHorizontalOverflow(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.scrollWidth,
      })),
    )
    .toEqual(
      await page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.clientWidth,
      })),
    );
}

test("@desktop @mobile Onboarding has accessible names, order, announcements, and contrast", async ({
  page,
}) => {
  await mockOnboarding(page);
  await page.goto("/");

  const heading = page.getByRole("heading", { name: "Welcome, Alex" });
  await expect(heading).toBeVisible();
  await expectAccessible(page);
  await expectNoHorizontalOverflow(page);

  await page.keyboard.press("Tab");
  const firstAcknowledgement = page.getByRole("checkbox", {
    name: /Private individual access/,
  });
  await expect(firstAcknowledgement).toBeFocused();
  await expect(firstAcknowledgement).toHaveCSS("outline-style", "solid");
  await expect(firstAcknowledgement).toHaveCSS("outline-width", "3px");
  await firstAcknowledgement.check();
  await expect(firstAcknowledgement).toBeChecked();
});

test("@desktop @mobile Recipient navigation, archives, and Media viewer are accessible", async ({
  page,
}) => {
  const browserRequests: string[] = [];
  page.on("request", (request) => browserRequests.push(request.url()));
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.addInitScript(() => {
    Object.defineProperty(window, "__mementoScrollBehaviors", {
      configurable: true,
      value: [] as ScrollBehavior[],
    });
    Element.prototype.scrollIntoView = function scrollIntoView(options) {
      const behavior =
        typeof options === "object" && options.behavior
          ? options.behavior
          : "auto";
      (
        window as typeof window & {
          __mementoScrollBehaviors: ScrollBehavior[];
        }
      ).__mementoScrollBehaviors.push(behavior);
    };
  });
  await mockRecipient(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
  await expectAccessible(page);
  await expectNoHorizontalOverflow(page);

  if ((page.viewportSize()?.width ?? 1280) <= 700) {
    const mobileNavigation = page.locator(".mobile-library-nav");
    for (const button of await mobileNavigation.getByRole("button").all()) {
      const bounds = await button.boundingBox();
      expect(bounds?.height).toBeGreaterThanOrEqual(44);
    }
    await page.getByLabel("Jump to date").selectOption({ index: 1 });
  } else {
    const dateSlider = page.getByRole("slider", { name: "Photo dates" });
    await expect(dateSlider).toHaveAttribute("aria-orientation", "vertical");
    await expect(dateSlider).toHaveAttribute(
      "aria-valuetext",
      "July 27, 2026, 1 photo",
    );
    await dateSlider.focus();
    await dateSlider.press("End");
    await expect(page).toHaveURL(/\/photos\?date=2026-06-15$/);
    await expect(dateSlider).toHaveAttribute(
      "aria-valuetext",
      "June 15, 2026, 1 photo",
    );
  }
  await expect(
    page.getByRole("heading", { name: "June 15, 2026" }),
  ).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as typeof window & {
              __mementoScrollBehaviors: ScrollBehavior[];
            }
          ).__mementoScrollBehaviors,
      ),
    )
    .toContain("auto");
  await page.goBack();
  await expect(page).toHaveURL(/\/$/);
  await expect(
    page.getByRole("heading", { name: "July 27, 2026" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Use light theme" }).click();
  await expectAccessible(page);

  const accountMenu = page.getByLabel("Account for Alex");
  await accountMenu.click();
  await expect(page.getByText("Review Onboarding")).toBeVisible();
  await expectAccessible(page);
  await accountMenu.click();

  const openMedia = page.getByRole("button", {
    name: "Open Photo 1 from July 2026",
  });
  await openMedia.click();
  const viewer = page.getByRole("dialog", { name: "Media viewer" });
  await expect(viewer).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Close viewer" }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Add Favorite" }).click();
  await expect(
    page.getByRole("button", { name: "Remove Favorite" }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("textbox", { name: "Add a Comment" }).fill("Wonderful");
  await page.getByRole("button", { name: "Post Comment" }).click();
  const commentList = page.locator(".comment-list");
  await expect(commentList).toHaveAttribute("aria-live", "polite");
  await expect(commentList).toContainText("Wonderful");
  if ((page.viewportSize()?.width ?? 1280) <= 700) {
    const downloadTarget = await page
      .getByRole("link", { name: "Download original" })
      .boundingBox();
    expect(downloadTarget?.height).toBeGreaterThanOrEqual(44);
  }
  await expectAccessible(page);
  await page.getByRole("button", { name: "Close viewer" }).click();
  await expect(openMedia).toBeFocused();

  const navigation =
    (page.viewportSize()?.width ?? 1280) <= 700
      ? page.locator(".mobile-library-nav")
      : page.locator(".library-rail");
  const eventsNavigation = navigation.getByRole("button", { name: "Events" });
  await page.keyboard.press("Tab");
  await eventsNavigation.focus();
  await expect(eventsNavigation).toHaveCSS("outline-style", "solid");
  await expect(eventsNavigation).toHaveCSS("outline-width", "3px");
  await eventsNavigation.click();
  await expect(page).toHaveURL(/\/events$/);
  await expect(page.getByRole("heading", { name: "Events" })).toBeFocused();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(page).toHaveURL(new RegExp(`/events/${eventID}$`));
  await expect(
    page.getByRole("heading", { name: "Family weekend" }),
  ).toBeFocused();
  await page.goBack();
  await expect(page).toHaveURL(/\/events$/);
  await expect(page.getByRole("heading", { name: "Events" })).toBeFocused();
  await page.goForward();
  await expect(page).toHaveURL(new RegExp(`/events/${eventID}$`));
  await expect(
    page.getByRole("heading", { name: "Family weekend" }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Prepare Event archive" }).click();
  await expect(
    page.getByRole("region", { name: "Archive downloads" }),
  ).toHaveAttribute("aria-live", "polite");
  await expectAccessible(page);

  if ((page.viewportSize()?.width ?? 1280) <= 700) {
    await navigation.getByRole("button", { name: "Search" }).click();
  } else {
    await page.getByRole("button", { name: "Search library" }).click();
  }
  await expect(page.getByRole("heading", { name: "Search" })).toBeFocused();
  const searchbox = page.getByRole("searchbox", {
    name: "Search published Events, Place labels, and People",
  });
  await searchbox.fill("family");
  await expect(searchbox).toHaveValue("family");
  const runSearch = page.getByRole("button", { name: "Run search" });
  await expect(runSearch).toBeEnabled();
  await runSearch.click();
  await expect(
    page.getByText("1 matching photo. 0 matching Events."),
  ).toBeVisible();
  await expectAccessible(page);

  await navigation.getByRole("button", { name: "Favorites" }).click();
  await expect(
    page.getByText("Favorites aren't shared with other recipients."),
  ).toBeVisible();
  await expectAccessible(page);

  await expect(page.getByText(/hidden/i)).toHaveCount(0);
  await expect(page.locator(".media-unavailable")).toHaveCount(0);
  const accessibilityTree = await page.locator("body").ariaSnapshot();
  expect(accessibilityTree).not.toContain("Hidden family photo");
  expect(accessibilityTree).not.toContain(inaccessibleMediaID);
  const browserArtifacts = await page.evaluate(async (hiddenID) => {
    const resourceURLs = Array.from(
      document.querySelectorAll<HTMLElement>("[href], [src]"),
    ).flatMap((element) =>
      [element.getAttribute("href"), element.getAttribute("src")].filter(
        (value): value is string => value !== null,
      ),
    );
    const storageValues = [localStorage, sessionStorage].flatMap((storage) =>
      Array.from({ length: storage.length }, (_, index) => {
        const key = storage.key(index) ?? "";
        return `${key}=${storage.getItem(key) ?? ""}`;
      }),
    );
    const cachedURLs = (
      await Promise.all(
        (await caches.keys()).map(async (name) =>
          (await caches.open(name))
            .keys()
            .then((requests) => requests.map((request) => request.url)),
        ),
      )
    ).flat();
    return {
      html: document.documentElement.outerHTML,
      leakedResource: resourceURLs.find((value) => value.includes(hiddenID)),
      leakedStorage: storageValues.find((value) => value.includes(hiddenID)),
      leakedCache: cachedURLs.find((value) => value.includes(hiddenID)),
    };
  }, inaccessibleMediaID);
  expect(browserArtifacts.html).not.toContain(inaccessibleMediaID);
  expect(browserArtifacts.html).not.toContain("Hidden family photo");
  expect(browserArtifacts.leakedResource).toBeUndefined();
  expect(browserArtifacts.leakedStorage).toBeUndefined();
  expect(browserArtifacts.leakedCache).toBeUndefined();
  expect(browserRequests.some((url) => url.includes(mediaID))).toBe(true);
  expect(browserRequests.some((url) => url.includes(inaccessibleMediaID))).toBe(
    false,
  );
  const applicationOrigin = new URL(page.url()).origin;
  expect(
    browserRequests
      .filter((url) => new URL(url).origin !== applicationOrigin)
      .map((url) => new URL(url).origin),
  ).toEqual([]);
});

test("@desktop @mobile Publication review and responsive drill-down are accessible", async ({
  page,
}) => {
  await mockPublication(page);
  await page.goto("/?workspace=drafts");
  await page.getByRole("button", { name: /Family weekend/ }).click();

  if ((page.viewportSize()?.width ?? 1280) <= 1024) {
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
    await expect(page.locator(".inspect-pane")).toBeFocused();
  }

  await expect(
    page.getByRole("button", { name: "Publish Event" }),
  ).toBeEnabled();
  await expectAccessible(page);
  await expectNoHorizontalOverflow(page);
});
