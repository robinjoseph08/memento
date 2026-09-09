import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

async function createPerson(page: Page, name: string) {
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
}

test("arranges a large Moment and reviews face-based access on desktop and mobile", async ({
  page,
  immich,
}) => {
  test.setTimeout(120_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await createPerson(page, "Alex");
  await createPerson(page, "Sam");

  await page.getByRole("link", { name: "Albums", exact: true }).click();
  await page.getByRole("link", { name: "Import an album" }).click();
  await page
    .getByRole("searchbox", { name: "Search Immich albums" })
    .fill("Workbench - Large Moment");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const source = page.getByRole("article").filter({
    has: page.getByRole("heading", {
      name: "Workbench - Large Moment",
      exact: true,
    }),
  });
  await source.getByRole("button", { name: "Import", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Moments", exact: true }),
  ).toBeVisible({ timeout: 60_000 });

  const firstMoment = page.getByRole("region", {
    name: "Thursday, June 4, 2026",
  });
  const media = firstMoment.getByRole("list", { name: "Moment media" });
  await expect(
    media.getByRole("img", { name: /workbench-\d+\.jpg/ }),
  ).toHaveCount(24);
  await firstMoment.getByRole("button", { name: "Show all 101 items" }).click();
  await expect(
    media.getByRole("img", { name: /workbench-\d+\.jpg/ }),
  ).toHaveCount(101);
  await firstMoment.getByRole("button", { name: "Show fewer items" }).click();

  for (const [faceName, personName] of [
    ["Immich Alex", "Alex"],
    ["Immich Alex duplicate", "Alex"],
  ] as const) {
    const form = page.getByRole("form", {
      name: `Link ${faceName}`,
      exact: true,
    });
    await expect(form).toBeVisible({ timeout: 30_000 });
    await form.getByRole("combobox", { name: "Existing Person" }).click();
    await page.getByRole("option", { name: personName, exact: true }).click();
    await form.getByRole("button", { name: "Link face" }).click();
    await expect(form).toHaveCount(0);
  }

  const desktopAccess = page.getByRole("complementary", {
    name: "Moment access",
  });
  const alexAccess = desktopAccess.getByRole("checkbox", {
    name: "Allow Alex for this Moment",
  });
  await expect(alexAccess).not.toBeChecked();
  await alexAccess.check();
  await expect(alexAccess).toBeChecked();
  await desktopAccess.getByRole("button", { name: "Undo" }).click();
  await expect(alexAccess).not.toBeChecked();
  await alexAccess.check();

  await firstMoment
    .getByRole("checkbox", { name: "Select workbench-002.jpg" })
    .check();
  await firstMoment.getByRole("button", { name: "Set as cover" }).click();
  const coverDialog = page.getByRole("dialog", {
    name: "Change Moment cover?",
  });
  await coverDialog.getByRole("button", { name: "Save cover" }).click();
  await expect(coverDialog).toHaveCount(0);
  await firstMoment.getByRole("button", { name: "Clear" }).click();

  await firstMoment
    .getByRole("checkbox", { name: "Select workbench-004.jpg" })
    .check();
  await firstMoment.getByRole("button", { name: "Split" }).click();
  const splitDialog = page.getByRole("dialog", { name: "Split Moment" });
  await splitDialog.getByRole("combobox", { name: "New Moment cover" }).click();
  await page
    .getByRole("option", { name: "workbench-004.jpg", exact: true })
    .click();
  await splitDialog.getByRole("button", { name: "Review split" }).click();
  await expect(
    splitDialog.getByRole("region", { name: "Visibility review" }),
  ).toContainText("No one gains or loses media");
  await splitDialog.getByRole("button", { name: "Confirm split" }).click();
  await expect(splitDialog).toHaveCount(0);

  const originalToggle = page.getByRole("button", {
    name: "June 4, 2026 (1)",
    exact: true,
  });
  const splitToggle = page.getByRole("button", {
    name: "June 4, 2026 (2)",
    exact: true,
  });
  await splitToggle.click();
  await desktopAccess
    .getByRole("checkbox", { name: "Allow Alex for this Moment" })
    .uncheck();
  await originalToggle.click();
  const original = page.getByRole("region", { name: "June 4, 2026 (1)" });
  await original
    .getByRole("checkbox", { name: "Select workbench-006.jpg" })
    .check();
  await original.getByRole("button", { name: "Move" }).click();
  const moveDialog = page.getByRole("dialog", { name: "Move media" });
  await moveDialog.getByRole("button", { name: "Review move" }).click();
  await expect(
    moveDialog.getByRole("region", { name: "Visibility review" }),
  ).toContainText("Alex loses 1");
  await moveDialog.getByRole("button", { name: "Confirm move" }).click();
  await expect(moveDialog).toHaveCount(0);

  await original.getByRole("button", { name: "Merge" }).click();
  const mergeDialog = page.getByRole("dialog", { name: "Merge Moments" });
  await mergeDialog
    .getByRole("combobox", { name: "Merged Moment cover" })
    .click();
  await page
    .getByRole("option", { name: "workbench-004.jpg", exact: true })
    .click();
  await mergeDialog.getByRole("button", { name: "Review merge" }).click();
  await mergeDialog.getByRole("combobox", { name: "Alex" }).click();
  await page.getByRole("option", { name: "Allow", exact: true }).click();
  await mergeDialog.getByRole("button", { name: "Review merge" }).click();
  await expect(
    mergeDialog.getByRole("region", { name: "Visibility review" }),
  ).toBeVisible();
  await mergeDialog.getByRole("button", { name: "Confirm merge" }).click();
  await expect(mergeDialog).toHaveCount(0);
  await expect(page.getByRole("region", { name: /June 4, 2026/ })).toHaveCount(
    1,
  );

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: /Access for June 4, 2026/ }).click();
  const sheet = page.getByRole("dialog", { name: "Moment access" });
  const mobileAlex = sheet.getByRole("checkbox", {
    name: "Allow Alex for this Moment",
  });
  await mobileAlex.uncheck();
  await sheet.getByRole("button", { name: "Undo" }).click();
  await expect(mobileAlex).toBeChecked();
  await sheet.getByRole("button", { name: "Close panel" }).click();
  await expect(sheet).toHaveCount(0);
});
