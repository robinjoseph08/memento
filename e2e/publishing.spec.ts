import { mkdir } from "node:fs/promises";
import type { Locator, Page, Request } from "@playwright/test";

import { expect, test } from "./fixtures";

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
  await page.getByRole("combobox", { name: "Preview as person" }).click();
  await page.getByRole("option", { name, exact: true }).click();
  await expect(
    page.getByRole("region", { name: "Preview identity" }),
  ).toContainText(`Previewing as ${name}`);
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
  const alexAlbum = albumAccess.getByRole("checkbox", {
    name: "Allow Alex for this Album",
  });
  await expect(alexAlbum).not.toBeChecked();
  // Quick choices render the committed response, not an optimistic checkbox.
  await alexAlbum.click();
  await expect(alexAlbum).toBeChecked();
  await albumAccess.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(alexAlbum).not.toBeChecked();
  await alexAlbum.click();
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
  ).toContainText("Inherit: allowed");
  await expect(
    rules.getByRole("combobox", { name: "Access for Sam" }),
  ).toContainText("Inherit: no access");
  await expect(
    rules.getByRole("button", { name: "Save access", exact: true }),
  ).toBeDisabled();
  for (const [person, decision] of [
    ["Alex", "Exclude"],
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
  const item = page.getByRole("dialog", { name: "Item access", exact: true });
  await expect(item).toBeVisible();
  const alexItem = item.getByRole("checkbox", {
    name: "Allow Alex for this item",
  });
  await expect(alexItem).toBeChecked();
  await alexItem.click();
  await expect(alexItem).not.toBeChecked();
  await item.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(alexItem).toBeChecked();
  await alexItem.click();
  await expect(alexItem).not.toBeChecked();
  await page.keyboard.press("Escape");
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
  await page.keyboard.press("Escape");

  await choosePreview(page, "Sam");
  await counts(page, 1, 0);
  await expect(
    page.getByRole("heading", { name: "No videos in this album" }),
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
    await expect(
      member.getByRole("heading", { name: "No albums yet", exact: true }),
    ).toBeVisible();
    await member.goto(viewerPath);
    await expect(
      member.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();

    await page.setViewportSize({ width: 390, height: 844 });
    await page
      .getByRole("link", { name: "Edit album access", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Album access", exact: true }),
    ).toBeVisible();
    await page.setViewportSize({ width: 1280, height: 900 });
    await page
      .getByRole("button", { name: "Review & publish", exact: true })
      .click();
    const publication = page.getByRole("dialog", {
      name: "Review & publish",
      exact: true,
    });
    await expect(
      publication.getByRole("listitem").filter({ hasText: "Alex" }),
    ).toContainText("2 photos, 2 videos");
    await expect(
      publication.getByRole("listitem").filter({ hasText: "Sam" }),
    ).toContainText("1 photo, 0 videos");
    await expect(publication).toContainText(/notification/i);
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
      .getByRole("button", { name: "Publish Album", exact: true })
      .click();
    await expect(publication).toHaveCount(0);

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
      .getByRole("button", { name: "Unpublish Album", exact: true })
      .click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Unpublish", exact: true })
      .click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
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
  } finally {
    await memberContext.close();
  }
});
