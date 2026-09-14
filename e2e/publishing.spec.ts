import { mkdir } from "node:fs/promises";
import type { Locator, Page, Request } from "@playwright/test";

import { expect, finishOnboarding, test } from "./fixtures";

async function captureLayouts(page: Page, name: string) {
  if (process.env.QA_CAPTURES !== "1") return;
  await mkdir("tmp/qa-captures", { recursive: true });
  const viewport = page.viewportSize();
  const prefix = `tmp/qa-captures/${test.info().project.name}-${name}`;
  await page.screenshot({ path: `${prefix}-desktop.png`, fullPage: true });
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: `${prefix}-mobile.png`, fullPage: true });
  } finally {
    if (viewport) await page.setViewportSize(viewport);
  }
}

async function createPerson(page: Page, name: string, email?: string) {
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  if (email) {
    await page
      .getByRole("textbox", { name: "Google email address" })
      .fill(email);
    await page.getByRole("button", { name: "Preauthorize email" }).click();
    await expect(
      page
        .getByRole("table", { name: "Preauthorizations", exact: true })
        .getByText(email, { exact: true }),
    ).toBeVisible();
  }
}

async function loadedImage(image: Locator) {
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate(
        (element: HTMLImageElement) =>
          element.complete && element.naturalWidth > 0,
      ),
    )
    .toBe(true);
  const source = await image.getAttribute("src");
  expect(source).toBeTruthy();
  return source!;
}

async function choosePreview(page: Page, name: string) {
  const chooser = page
    .getByRole("region", { name: "Preview identity" })
    .getByRole("combobox", { name: "Preview as" });
  await chooser.click();
  await page.getByRole("option", { name, exact: true }).click();
  await expect(chooser).toHaveText(name);
}

async function counts(page: Page, photos: number, videos: number) {
  const tabs = page.getByRole("navigation", { name: "Album media" });
  await expect(
    tabs.getByRole("link", { name: `Photos ${photos}`, exact: true }),
  ).toBeVisible();
  await expect(
    tabs.getByRole("link", { name: `Videos ${videos}`, exact: true }),
  ).toBeVisible();
}

test("Curator scopes access, previews two people, publishes, and hides the Album without losing preview", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(120_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await createPerson(page, "Alex", "alex@example.test");
  await createPerson(page, "Sam");
  await page.getByRole("link", { name: "Albums", exact: true }).click();
  await page.getByRole("link", { name: "Import an album" }).click();
  await page
    .getByRole("searchbox", { name: "Search Immich albums" })
    .fill("Fixture Album - Coast");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page
    .getByRole("article")
    .filter({
      has: page.getByRole("heading", {
        name: "Fixture Album - Coast",
        exact: true,
      }),
    })
    .getByRole("button", { name: "Import", exact: true })
    .click();
  const outline = page.getByRole("navigation", { name: "Album outline" });
  await expect(
    outline.getByRole("link", { name: /Monday, June 1, 2026/ }),
  ).toBeVisible({ timeout: 60_000 });
  const editorURL = new URL(page.url());
  const viewerPath = editorURL.pathname.replace("/curator", "");

  await outline
    .getByRole("link", { name: "Album access", exact: true })
    .click();
  const albumAccess = page.getByRole("form", {
    name: "Album access",
    exact: true,
  });
  // No faces are linked yet, so everyone sits under the collapsed list.
  await albumAccess.getByText(/not seen in this album/).click();
  const alexAlbum = albumAccess.getByRole("checkbox", {
    name: "Album access for Alex",
  });
  await expect(alexAlbum).not.toBeChecked();
  const visibility = albumAccess.getByRole("region", {
    name: "Visibility review",
  });
  await expect(visibility).toContainText("Change access above to review it.");
  await alexAlbum.check();
  await expect(visibility).toContainText("Alex");
  await expect(visibility).toContainText("gains 6");
  await captureLayouts(page, "album-access");
  await albumAccess
    .getByRole("button", { name: "Save Album access", exact: true })
    .click();
  await expect(albumAccess.getByRole("status")).toHaveText(
    "Album access saved.",
  );
  await expect(alexAlbum).toBeChecked();

  await outline.getByRole("link", { name: /Monday, June 1, 2026/ }).click();
  await page
    .getByRole("button", { name: "Rules & exceptions", exact: true })
    .click();
  const rules = page.getByRole("dialog", {
    name: "Rules & exceptions",
    exact: true,
  });
  await expect(
    rules.getByRole("combobox", { name: "Access for Alex" }),
  ).toContainText("Inherit: allowed by Album access");
  await expect(
    rules.getByRole("combobox", { name: "Access for Sam" }),
  ).toContainText("Inherit: no access");
  for (const [person, decision] of [
    ["Alex", "Deny"],
    ["Sam", "Allow"],
  ]) {
    await rules.getByRole("combobox", { name: `Access for ${person}` }).click();
    await page.getByRole("option", { name: decision, exact: true }).click();
  }
  await captureLayouts(page, "rules");
  await rules.getByRole("button", { name: "Save access", exact: true }).click();
  await expect(rules).toHaveCount(0);

  await outline.getByRole("link", { name: /Tuesday, June 2, 2026/ }).click();
  // Outside Select mode, the tile opens its item access editor.
  await page.getByRole("img", { name: "coast-03.jpg", exact: true }).click();
  const item = page.getByRole("dialog", { name: "Photo details", exact: true });
  await expect(item).toBeVisible();
  const alexItem = item.getByRole("combobox", { name: "Access for Alex" });
  await expect(alexItem).toContainText("Inherit: allowed by Album access");
  await alexItem.click();
  await page.getByRole("option", { name: "Deny", exact: true }).click();
  await captureLayouts(page, "item-access");
  await item.getByRole("button", { name: "Save access", exact: true }).click();
  await expect(item).toHaveCount(0);

  await outline
    .getByRole("link", { name: "Viewer preview", exact: true })
    .click();
  await choosePreview(page, "Alex");
  await counts(page, 2, 2);
  const alexCover = await loadedImage(
    page.getByRole("img", { name: "Album cover", exact: true }),
  );
  const alexPhoto = await loadedImage(
    page.getByRole("img", { name: "coast-05", exact: true }),
  );
  expect(alexCover).toBe(alexPhoto);
  await loadedImage(page.getByRole("img", { name: "coast-02", exact: true }));
  await expect(page.getByRole("img", { name: /coast-0[13]/ })).toHaveCount(0);
  await captureLayouts(page, "preview-alex");
  await page.getByRole("link", { name: "Videos 2", exact: true }).click();
  await expect(page).toHaveURL(/tab=videos/);
  await loadedImage(page.getByRole("img", { name: "coast-04", exact: true }));
  await loadedImage(page.getByRole("img", { name: "coast-06", exact: true }));
  await page.getByRole("button", { name: "Account menu" }).click();
  await expect(
    page.getByRole("menuitem", { name: "Profile", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("menuitem", { name: "Sign out", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("menuitemcheckbox", { name: "Dark mode", exact: true }),
  ).toBeEnabled();
  await page.keyboard.press("Escape");

  await choosePreview(page, "Sam");
  await counts(page, 1, 0);
  await expect(
    page.getByText("No videos are shared with Sam yet."),
  ).toBeVisible();
  await expect(page.getByRole("img", { name: /coast-0[2456]/ })).toHaveCount(0);
  const samCover = await loadedImage(
    page.getByRole("img", { name: "Album cover", exact: true }),
  );
  expect(samCover).not.toBe(alexCover);
  await page.getByRole("link", { name: "Photos 1", exact: true }).click();
  expect(
    await loadedImage(page.getByRole("img", { name: "coast-01", exact: true })),
  ).toBe(samCover);
  await captureLayouts(page, "preview-sam");
  await choosePreview(page, "Alex");
  await counts(page, 2, 2);
  await expect(
    page.getByRole("img", { name: "Album cover", exact: true }),
  ).toHaveAttribute("src", alexCover);
  await expect(
    page.getByRole("img", { name: "coast-01", exact: true }),
  ).toHaveCount(0);

  const memberContext = await browser.newContext({ baseURL });
  try {
    const member = await memberContext.newPage();
    await member.goto("/sign-in");
    await member
      .getByRole("textbox", { name: "Email", exact: true })
      .fill("alex@example.test");
    await member.getByRole("button", { name: "Sign in", exact: true }).click();
    await finishOnboarding(member);
    await expect(
      member.getByRole("heading", { name: "No albums yet", exact: true }),
    ).toBeVisible();
    await member.goto(viewerPath);
    await expect(
      member.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();

    // Narrow screens drill back to the outline instead of a side sheet.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("link", { name: "Outline", exact: true }).click();
    await outline
      .getByRole("link", { name: "Album access", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Album access", exact: true }),
    ).toBeVisible();
    await page.setViewportSize({ width: 1280, height: 900 });
    await page
      .getByRole("button", { name: "Review & publish", exact: true })
      .click();
    const publication = page.getByRole("dialog", {
      name: "Ready to publish?",
      exact: true,
    });
    const audience = publication.getByRole("region", {
      name: "Audience",
      exact: true,
    });
    await expect(audience).toContainText(/Alex\s*4 of 6 items/);
    await expect(audience).toContainText(/Sam\s*1 of 6 items/);
    await expect(publication).toContainText("6 items in 3 Moments");
    await expect(publication).toContainText("sends no notifications");
    await captureLayouts(page, "publication");
    const publicationWrites: string[] = [];
    const recordPublicationWrite = (request: Request) => {
      if (!["GET", "HEAD", "OPTIONS"].includes(request.method())) {
        publicationWrites.push(
          `${request.method()} ${new URL(request.url()).pathname}`,
        );
      }
    };
    page.on("request", recordPublicationWrite);
    await publication
      .getByRole("button", { name: "Publish album", exact: true })
      .click();
    await expect(publication).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Review & publish", exact: true }),
    ).toHaveCount(0);

    await member.reload();
    await counts(member, 2, 2);
    page.off("request", recordPublicationWrite);
    // No notification API exists yet. Assert publication sends only its own
    // mutation, rather than checking an invented notification endpoint.
    expect(publicationWrites).toEqual([
      `POST /api${editorURL.pathname}/publish`,
    ]);
    const memberPhoto = await loadedImage(
      member.getByRole("img", { name: "coast-05", exact: true }),
    );
    expect(
      await loadedImage(
        member.getByRole("img", { name: "Album cover", exact: true }),
      ),
    ).toBe(memberPhoto);
    expect(memberPhoto).not.toBe(alexCover);
    await loadedImage(
      member.getByRole("img", { name: "coast-02", exact: true }),
    );
    await expect(member.getByRole("img", { name: /coast-0[13]/ })).toHaveCount(
      0,
    );
    await expect(
      member.getByRole("link", { name: "People", exact: true }),
    ).toHaveCount(0);
    await captureLayouts(member, "viewer-alex");

    await outline
      .getByRole("link", { name: "Album details", exact: true })
      .click();
    await page
      .getByRole("region", { name: "Danger zone", exact: true })
      .getByRole("button", { name: "Unpublish Album", exact: true })
      .click();
    await page
      .getByRole("dialog", { name: "Unpublish Album?", exact: true })
      .getByRole("button", { name: "Unpublish Album", exact: true })
      .click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Review & publish", exact: true }),
    ).toBeVisible();
    await member.reload();
    await expect(
      member.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();
    // APIRequestContext bypasses the browser's immutable image cache.
    expect((await member.request.get(memberPhoto)).status()).toBe(404);
    await member.getByRole("link", { name: "Albums", exact: true }).click();
    await expect(
      member.getByRole("heading", { name: "No albums yet", exact: true }),
    ).toBeVisible();
    await outline
      .getByRole("link", { name: "Viewer preview", exact: true })
      .click();
    await choosePreview(page, "Alex");
    await counts(page, 2, 2);
    await loadedImage(page.getByRole("img", { name: "coast-05", exact: true }));

    // Remove all of Sam's decisions after a visibility review, then delete
    // the Album with a typed title. Immich is never written to.
    await outline
      .getByRole("link", { name: "Album access", exact: true })
      .click();
    await albumAccess.getByText(/not seen in this album/).click();
    await albumAccess
      .getByRole("button", { name: "Remove all access…", exact: true })
      .nth(1)
      .click();
    const removal = page.getByRole("dialog", {
      name: "Remove all access for Sam?",
      exact: true,
    });
    await expect(
      removal.getByRole("region", { name: "Visibility review" }),
    ).toContainText(/Sam\s*loses 1/);
    await removal
      .getByRole("button", { name: "Remove all access", exact: true })
      .click();
    await expect(removal).toHaveCount(0);
    await choosePreviewFromOutline(page, outline, "Sam");
    await expect(
      page.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();

    await outline
      .getByRole("link", { name: "Album details", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Delete Album", exact: true })
      .click();
    const deletion = page.getByRole("dialog", {
      name: "Permanently delete this Album?",
      exact: true,
    });
    await deletion
      .getByRole("textbox", { name: "Type the Album title" })
      .fill("Fixture Album - Coast");
    await deletion
      .getByRole("button", { name: "Permanently delete Album", exact: true })
      .click();
    await expect(page).toHaveURL(/\/curator\/albums$/);
    await expect(
      page.getByRole("heading", { name: "No albums yet", exact: true }),
    ).toBeVisible();
  } finally {
    await memberContext.close();
  }
});

async function choosePreviewFromOutline(
  page: Page,
  outline: Locator,
  name: string,
) {
  await outline
    .getByRole("link", { name: "Viewer preview", exact: true })
    .click();
  await choosePreview(page, name);
}
