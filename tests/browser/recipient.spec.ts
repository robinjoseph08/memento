import { expect, test, type Page } from "@playwright/test";

const media = {
  id: "11111111-1111-4111-8111-111111111111",
  media_type: "image",
  width: 1600,
  height: 900,
  local_date_time: "2026-07-27T12:00:00Z",
  capture_date: "2026-07-27",
  available: true,
  thumbnail_url: "/api/me/media/11111111-1111-4111-8111-111111111111/thumbnail",
  preview_url: "/api/me/media/11111111-1111-4111-8111-111111111111/preview",
  video_url: "",
  original_url: "/api/me/media/11111111-1111-4111-8111-111111111111/original",
};

const secondMedia = {
  ...media,
  id: "44444444-4444-4444-8444-444444444444",
  media_type: "video",
  width: 900,
  height: 1600,
  local_date_time: "2026-07-27T13:00:00Z",
  thumbnail_url: "/api/me/media/44444444-4444-4444-8444-444444444444/thumbnail",
  preview_url: "/api/me/media/44444444-4444-4444-8444-444444444444/preview",
  video_url: "/api/me/media/44444444-4444-4444-8444-444444444444/video",
  original_url: "/api/me/media/44444444-4444-4444-8444-444444444444/original",
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

async function expectClosedViewerHidden(page: Page) {
  await page.evaluate(() => {
    const dialog = document.createElement("dialog");
    dialog.className = "media-viewer";
    dialog.dataset.closedViewerProbe = "";
    document.body.append(dialog);
  });
  const closedViewer = page.locator("dialog[data-closed-viewer-probe]");
  await expect(closedViewer).toHaveCount(1);
  await expect(closedViewer).toBeHidden();
  await closedViewer.evaluate((dialog) => dialog.remove());
}

async function recipientAPI(
  page: Page,
  sessionType: "trusted" | "public" = "trusted",
) {
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
          session_type: sessionType,
          csrf_token: "c".repeat(64),
          curator: false,
          onboarding_required: false,
        },
      });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204, body: "" });
    } else if (
      path === "/api/me/photos/chronology" ||
      path === "/api/me/favorites/chronology"
    ) {
      await route.fulfill({
        json: {
          dates: [{ capture_date: "2026-07-27", media_count: 2, cursor: "" }],
        },
      });
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
      path === media.preview_url ||
      path === secondMedia.thumbnail_url
    ) {
      await route.fulfill({
        contentType: "image/png",
        body: Buffer.from(
          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
          "base64",
        ),
      });
    } else if (
      path === `/api/favorites/${media.id}` &&
      request.method() === "GET"
    ) {
      await route.fulfill({
        json: { media_item_id: media.id, favorite: false },
      });
    } else if (
      path === `/api/comments/media/${media.id}` &&
      request.method() === "GET"
    ) {
      await route.fulfill({
        json: {
          comments: [],
          can_mute: false,
          muted: false,
          next_cursor: null,
        },
      });
    } else if (path === "/api/me/archives" && request.method() === "POST") {
      const payload = request.postDataJSON() as { scope: string };
      await route.fulfill({
        status: 201,
        json: {
          name:
            payload.scope === "event" ? "Family-weekend" : "Memento-selection",
          item_count: 1,
          total_size: 3,
          expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
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
    } else if (
      path === "/api/me/archives/parts/1" &&
      url.searchParams.get("token") === "private-token"
    ) {
      expect(request.method()).toBe("POST");
      expect(request.headers()["x-memento-csrf"]).toBe("c".repeat(64));
      await route.fulfill({
        contentType: "application/zip",
        headers: {
          "Content-Disposition": 'attachment; filename="Family-weekend.zip"',
        },
        body: Buffer.from("zip"),
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
    } else if (path === "/api/me/people/search") {
      await route.fulfill({ json: { people: [], next_cursor: null } });
    } else {
      await route.fulfill({
        status: 404,
        json: { error: { message: "not found" } },
      });
    }
  });
}

test("@desktop @mobile Interest-list search is private, paginated, and accessible", async ({
  page,
}) => {
  await recipientAPI(page);
  const visible = {
    id: "55555555-5555-4555-8555-555555555555",
    display_name: "José Alvarez",
    sort_name: "Alvarez, José",
    relationship: { connection_type: "sibling" },
  };
  const second = {
    id: "66666666-6666-4666-8666-666666666666",
    display_name: "Blair Visible",
    sort_name: "Visible, Blair",
  };
  const searches: Array<{
    url: string;
    method: string;
    headers: Record<string, string>;
    body: { query?: string; cursor?: string; limit?: number };
  }> = [];
  await page.route("**/api/me/people/search", async (route) => {
    const request = route.request();
    const body = request.postDataJSON() as {
      query?: string;
      cursor?: string;
      limit?: number;
    };
    searches.push({
      url: request.url(),
      method: request.method(),
      headers: request.headers(),
      body,
    });
    if (body.query === "missing") {
      await route.fulfill({ json: { people: [], next_cursor: null } });
    } else if (body.cursor === "directory-next") {
      await route.fulfill({ json: { people: [second], next_cursor: null } });
    } else {
      await route.fulfill({
        json: { people: [visible], next_cursor: "directory-next" },
      });
    }
  });
  await page.route(`**/api/me/interest-list/${visible.id}`, async (route) => {
    const request = route.request();
    expect(request.method()).toBe("PUT");
    expect(request.headers()["x-memento-csrf"]).toBe("c".repeat(64));
    expect(request.postDataJSON()).toEqual({ selected: true, version: 0 });
    await route.fulfill({
      json: {
        recipient: {
          id: "recipient",
          display_name: "Alex",
          sort_name: "alex",
        },
        version: 1,
        entries: [
          {
            person: visible,
            state: "active",
            chosen_at: "2026-07-31T12:00:00Z",
            updated_at: "2026-07-31T12:00:00Z",
          },
        ],
        history: [],
      },
    });
  });

  await page.goto("/");
  expect(searches).toHaveLength(0);
  await page.getByLabel("Account for Alex").click();
  const searchbox = page.getByRole("searchbox", {
    name: "Search People available for your Interest list",
  });
  await expect(searchbox).toBeVisible();
  await searchbox.fill("jose");
  await searchbox.press("Enter");
  await expect(searchbox).toBeFocused();
  await expect(
    page.getByRole("checkbox", { name: "José Alvarez" }),
  ).toBeVisible();
  await expect(page.getByText("sibling", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Load more People" }).click();
  await expect(
    page.getByRole("checkbox", { name: "Blair Visible" }),
  ).toBeVisible();
  await page.getByRole("checkbox", { name: "José Alvarez" }).click();
  await expect(
    page.getByRole("checkbox", { name: "José Alvarez" }),
  ).toBeChecked();
  await expect(page.getByText("Selected explicitly")).toBeVisible();

  expect(searches.some(({ body }) => body.query === "jose")).toBe(true);
  expect(searches.some(({ body }) => body.cursor === "directory-next")).toBe(
    true,
  );
  for (const search of searches) {
    expect(search.method).toBe("POST");
    expect(new URL(search.url).search).toBe("");
    expect(search.headers["x-memento-csrf"]).toBeUndefined();
    expect(search.body.limit).toBe(25);
  }

  await searchbox.fill("missing");
  await searchbox.press("Enter");
  await expect(
    page.getByText("No People are available for this search."),
  ).toBeVisible();
  await expect(searchbox).toBeFocused();
  for (const privateText of [
    "private@example.test",
    "Private Family Circle",
    "Hidden Intermediary",
  ]) {
    await expect(page.getByText(privateText, { exact: false })).toHaveCount(0);
  }
  const searchButton = page
    .getByRole("search")
    .getByRole("button", { name: "Search", exact: true });
  const box = await searchButton.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.height).toBeGreaterThanOrEqual(40);

  await page.getByLabel("Account for Alex").click();
  await expect(searchbox).toHaveCount(0);
  await expect(page.locator('input[value="missing"]')).toHaveCount(0);
  const searchesBeforeReopen = searches.length;
  await page.getByLabel("Account for Alex").click();
  const reopenedSearchbox = page.getByRole("searchbox", {
    name: "Search People available for your Interest list",
  });
  await expect(reopenedSearchbox).toHaveValue("");
  await expect
    .poll(() => searches.length)
    .toBeGreaterThan(searchesBeforeReopen);
  expect(searches.at(-1)?.body.query).toBe("");
});

test("@desktop @mobile complete chronology jumps directly beyond the first page", async ({
  page,
}) => {
  await recipientAPI(page);
  const distantMedia = {
    ...media,
    id: "55555555-5555-4555-8555-555555555555",
    capture_date: "2010-04-03",
    local_date_time: "2010-04-03T12:00:00Z",
    thumbnail_url:
      "/api/me/media/55555555-5555-4555-8555-555555555555/thumbnail",
    preview_url: "/api/me/media/55555555-5555-4555-8555-555555555555/preview",
    original_url: "/api/me/media/55555555-5555-4555-8555-555555555555/original",
  };
  const listingRequests: string[] = [];
  await page.route("**/api/me/photos/chronology", async (route) => {
    await route.fulfill({
      json: {
        dates: [
          {
            capture_date: "2026-07-27",
            media_count: 80,
            cursor: "latest-anchor",
          },
          {
            capture_date: "2010-04-03",
            media_count: 1,
            cursor: "distant-anchor",
          },
        ],
      },
    });
  });
  await page.route("**/api/me/photos?*", async (route) => {
    const url = new URL(route.request().url());
    listingRequests.push(url.toString());
    const cursor = url.searchParams.get("cursor");
    if (cursor === "latest-anchor") {
      await route.fulfill({
        json: { media: [media], next_cursor: "unrequested-predecessor" },
      });
      return;
    }
    if (cursor === "distant-anchor") {
      await route.fulfill({
        json: { media: [distantMedia], next_cursor: null },
      });
      return;
    }
    await route.fulfill({
      status: 400,
      json: { error: { message: `Unexpected cursor ${cursor ?? "none"}` } },
    });
  });

  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "July 27, 2026" }),
  ).toBeVisible();
  const mobile = (page.viewportSize()?.width ?? 1280) <= 700;
  if (mobile) {
    await page
      .getByRole("combobox", { name: "Jump to date" })
      .selectOption("2010-04-03");
  } else {
    const rail = page.getByRole("slider", { name: "Photo dates" });
    const railBounds = await rail.boundingBox();
    expect(railBounds).not.toBeNull();
    const distantPosition = {
      x: railBounds!.width / 2,
      y: railBounds!.height * 0.75,
    };
    const requestsBeforeHover = listingRequests.length;
    await rail.hover({ position: distantPosition });
    await expect(page.getByText("April 3, 2010 · 1 photo")).toBeVisible();
    expect(listingRequests).toHaveLength(requestsBeforeHover);
    await rail.click({ position: distantPosition });
  }

  await expect(
    page.getByRole("heading", { name: "April 3, 2010" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/photos\?date=2010-04-03$/);
  expect(
    listingRequests.some(
      (request) =>
        new URL(request).searchParams.has("cursor") &&
        new URL(request).searchParams.get("cursor") ===
          "unrequested-predecessor",
    ),
  ).toBe(false);
  expect(
    listingRequests.some(
      (request) =>
        new URL(request).searchParams.get("cursor") === "distant-anchor",
    ),
  ).toBe(true);

  await page.goBack();
  await expect(page).toHaveURL(/\/$/);
  await expect(
    page.getByRole("heading", { name: "July 27, 2026" }),
  ).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(/\/photos\?date=2010-04-03$/);
  await expect(
    page.getByRole("heading", { name: "April 3, 2010" }),
  ).toBeVisible();

  await expect(
    page.getByText("Hidden family date", { exact: false }),
  ).toHaveCount(0);
});

test("@desktop @mobile Recipient lands on Photos and sees only filtered Event totals", async ({
  page,
}) => {
  await recipientAPI(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "New for you" }),
  ).toBeVisible();
  const mobile = (page.viewportSize()?.width ?? 1280) <= 700;
  const primaryNavigation = page.locator(
    mobile ? ".mobile-library-nav" : ".library-rail",
  );
  if (mobile) {
    await expect(page.locator(".library-rail")).toBeHidden();
    await expect(page.locator(".mobile-library-nav")).toBeVisible();
  } else {
    await expect(page.locator(".library-rail")).toBeVisible();
    await expect(page.locator(".mobile-library-nav")).toBeHidden();
  }
  await expect(page.getByText("hidden", { exact: false })).toHaveCount(0);
  await expectClosedViewerHidden(page);

  await page.getByRole("button", { name: "Select photos" }).click();
  await page.getByRole("checkbox", { name: /Select Photo 1/ }).check();
  await page
    .getByRole("button", { name: "Prepare archive for 1 selected" })
    .click();
  const subsetArchive = page.getByRole("button", {
    name: "Download archive",
  });
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    subsetArchive.click(),
  ]);
  expect(download.suggestedFilename()).toBe("Family-weekend.zip");
  await expect(
    page.getByRole("button", { name: "Downloaded archive" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Cancel selection" }).click();
  await expect(
    page.getByRole("button", { name: "Select photos" }),
  ).toBeFocused();

  await page.getByRole("button", { name: "Load more photos" }).click();
  await expect(page.getByAltText("Video 2 from July 2026")).toBeVisible();
  await expect(page.getByAltText("Photo 1 from July 2026")).toBeVisible();
  const opener = page.getByRole("button", {
    name: "Open Photo 1 from July 2026",
  });
  await opener.click();
  const viewer = page.getByRole("dialog", { name: "Media viewer" });
  const closeViewer = page.getByRole("button", { name: "Close viewer" });
  const downloadOriginal = page.getByRole("link", {
    name: "Download original",
  });
  await expect(viewer).toBeVisible();
  await expect(viewer).toHaveAttribute("aria-modal", "true");
  await expect(closeViewer).toBeFocused();
  await expect(page.getByAltText("Selected photo preview")).toHaveAttribute(
    "src",
    media.preview_url,
  );
  await expect(downloadOriginal).toHaveAttribute("href", media.original_url);
  await expect(
    page.getByRole("button", { name: "Add Favorite" }),
  ).toBeEnabled();
  await expect(
    page.getByRole("textbox", { name: "Add a Comment" }),
  ).toBeEnabled();
  await expect(
    page.getByText("This Media is no longer available in your Library."),
  ).toHaveCount(0);

  await primaryNavigation
    .getByRole("button", { name: "Events" })
    .evaluate((button: HTMLButtonElement) => button.focus());
  await expect(closeViewer).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(downloadOriginal).toBeFocused();
  for (const key of ["Tab", "Tab", "Shift+Tab", "Shift+Tab"]) {
    await page.keyboard.press(key);
    const focus = await viewer.evaluate((dialog) => ({
      inside: dialog.contains(document.activeElement),
      active:
        document.activeElement?.getAttribute("aria-label") ??
        document.activeElement?.tagName ??
        "none",
    }));
    expect(focus.inside, `${key} focused ${focus.active}`).toBe(true);
  }

  await closeViewer.click();
  await expect(viewer).toBeHidden();
  await expect(opener).toBeFocused();

  await primaryNavigation.getByRole("button", { name: "Events" }).click();
  await expect(page.getByText("1 item")).toBeVisible();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(page.getByText("1 item")).toBeVisible();
  await expect(page.getByText(/Moment/)).toHaveCount(0);
  await page.getByRole("button", { name: "Prepare Event archive" }).click();
  await expect(
    page.getByRole("button", { name: "Download archive" }),
  ).toBeEnabled();
  await expect(page.getByAltText("Photo 1 from July 2026")).toHaveAttribute(
    "src",
    media.thumbnail_url,
  );
  await page
    .getByRole("button", { name: "Open Photo 1 from July 2026" })
    .click();
  await expect(page.getByAltText("Selected photo preview")).toHaveAttribute(
    "src",
    media.preview_url,
  );
  await expect(
    page.getByRole("link", { name: "Download original" }),
  ).toHaveAttribute("href", media.original_url);
});

test("@desktop @mobile public-computer original, subset, and Event archives show persistent-file warnings", async ({
  page,
}) => {
  await recipientAPI(page, "public");
  await page.goto("/");

  await page
    .getByRole("button", { name: "Open Photo 1 from July 2026" })
    .click();
  await expect(
    page.getByText(
      "This original will remain on this public computer after sign-out.",
    ),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close viewer" }).click();

  await page.getByRole("button", { name: "Select photos" }).click();
  await expect(
    page.getByText(
      "Subset archive files will remain on this public computer after sign-out.",
    ),
  ).toBeVisible();
  const primaryNavigation = page.locator(
    (page.viewportSize()?.width ?? 1280) <= 700
      ? ".mobile-library-nav"
      : ".library-rail",
  );
  await primaryNavigation.getByRole("button", { name: "Events" }).click();
  await page.getByRole("button", { name: /Family weekend/ }).click();
  await expect(
    page.getByText(
      "Event archive files will remain on this public computer after sign-out.",
    ),
  ).toBeVisible();
});

test("@mobile complete thumbnails and compact navigation do not expose inaccessible totals", async ({
  page,
}) => {
  await recipientAPI(page);
  await page.goto("/");

  const thumbnail = page.getByAltText("Photo 1 from July 2026");
  await expect(thumbnail).toBeVisible();
  await expect(thumbnail).toHaveCSS("object-fit", "contain");
  await expect(page.locator(".mobile-library-nav")).toBeVisible();
  await expect(page.locator(".library-rail")).toBeHidden();
  await expect(page.getByLabel("Jump to date")).toBeVisible();
  await expect(page.getByText(/total/i)).toHaveCount(0);
  await expectClosedViewerHidden(page);

  await page
    .getByRole("button", { name: "Open Photo 1 from July 2026" })
    .click();
  const viewer = page.getByRole("dialog", { name: "Media viewer" });
  await expect(viewer).toBeVisible();
  await expect(viewer).toHaveCSS("overflow-y", "auto");
  expect(
    await viewer.evaluate(
      (element) => element.scrollWidth <= element.clientWidth,
    ),
  ).toBe(true);

  const download = page.getByRole("link", { name: "Download original" });
  await download.scrollIntoViewIfNeeded();
  await expect(download).toBeVisible();
  await expect(download).toHaveAttribute("href", media.original_url);
  await expect(
    page.getByRole("button", { name: "Add Favorite" }),
  ).toBeEnabled();
  await expect(
    page.getByText("This Media is no longer available in your Library."),
  ).toHaveCount(0);
  const downloadBox = await download.boundingBox();
  const viewport = page.viewportSize();
  expect(downloadBox).not.toBeNull();
  expect(viewport).not.toBeNull();
  expect(downloadBox!.x).toBeGreaterThanOrEqual(0);
  expect(downloadBox!.x + downloadBox!.width).toBeLessThanOrEqual(
    viewport!.width,
  );
  expect(downloadBox!.y).toBeGreaterThanOrEqual(0);
  expect(downloadBox!.y + downloadBox!.height).toBeLessThanOrEqual(
    viewport!.height,
  );

  const close = page.getByRole("button", { name: "Close viewer" });
  await close.scrollIntoViewIfNeeded();
  await expect(close).toBeVisible();
  await close.click();
  await expect(viewer).toBeHidden();

  await page
    .locator(".mobile-library-nav")
    .getByRole("button", { name: "Favorites" })
    .click();
  await expect(
    page.getByText("Favorites aren't shared with other recipients."),
  ).toBeVisible();
});
