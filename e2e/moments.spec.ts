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
    page.getByRole("heading", { name: "Thursday, June 4, 2026", exact: true }),
  ).toBeVisible({ timeout: 60_000 });

  const outline = page.getByRole("navigation", { name: "Album outline" });
  const firstMoment = page.getByRole("region", {
    name: "Thursday, June 4, 2026",
  });
  const media = firstMoment.getByRole("list", { name: "Moment media" });
  await expect(
    media.getByRole("img", { name: /workbench-\d+\.jpg/ }),
  ).toHaveCount(24);
  await firstMoment.getByRole("button", { name: "Show all 101" }).click();
  await expect(
    media.getByRole("img", { name: /workbench-\d+\.jpg/ }),
  ).toHaveCount(101);
  await firstMoment.getByRole("button", { name: "Show fewer" }).click();

  await firstMoment.getByText(/unlinked faces? to link/).click();
  for (const [faceName, personName] of [
    ["Immich Alex", "Alex"],
    ["Immich Alex duplicate", "Alex"],
  ] as const) {
    await page
      .getByRole("button", { name: `Link ${faceName}`, exact: true })
      .click({ timeout: 30_000 });
    const form = page.getByRole("form", {
      name: `Link ${faceName}`,
      exact: true,
    });
    await expect(form).toBeVisible();
    await form.getByRole("combobox", { name: "Person" }).click();
    await page.getByRole("option", { name: personName, exact: true }).click();
    await form.getByRole("button", { name: "Link face" }).click();
    await expect(form).toHaveCount(0);
  }

  const desktopAccess = page.getByRole("region", { name: "Moment access" });
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
    .getByRole("button", { name: "Select", exact: true })
    .click();
  await firstMoment
    .getByRole("checkbox", { name: "Select workbench-002.jpg" })
    .check();
  await firstMoment.getByRole("button", { name: "Set as cover" }).click();
  const coverDialog = page.getByRole("dialog", {
    name: "Change Moment cover?",
  });
  await coverDialog.getByRole("button", { name: "Save cover" }).click();
  await expect(coverDialog).toHaveCount(0);
  await firstMoment.getByRole("button", { name: "Done" }).click();

  await firstMoment
    .getByRole("button", { name: "Select", exact: true })
    .click();
  await firstMoment
    .getByRole("checkbox", { name: "Select workbench-004.jpg" })
    .check();
  await firstMoment.getByRole("button", { name: "Split" }).click();
  const splitDialog = page.getByRole("dialog", { name: "Split Moment" });
  await expect(
    splitDialog.getByRole("combobox", { name: /cover/ }),
  ).toHaveCount(0);
  await splitDialog.getByRole("button", { name: "Review split" }).click();
  await expect(
    splitDialog.getByRole("region", { name: "Visibility review" }),
  ).toContainText("No one gains or loses media");
  await splitDialog.getByRole("button", { name: "Confirm split" }).click();
  await expect(splitDialog).toHaveCount(0);

  const originalRow = outline.getByRole("link", {
    name: /^June 4, 2026 \(1\)/,
  });
  const splitRow = outline.getByRole("link", { name: /^June 4, 2026 \(2\)/ });
  await splitRow.click();
  await desktopAccess
    .getByRole("checkbox", { name: "Allow Alex for this Moment" })
    .uncheck();
  await originalRow.click();
  const original = page.getByRole("region", { name: "June 4, 2026 (1)" });
  await original.getByRole("button", { name: "Select", exact: true }).click();
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
  const keepOriginalCover = mergeDialog.getByRole("radio", {
    name: "Keep the cover of June 4, 2026 (1)",
  });
  await expect(
    mergeDialog.getByRole("radio", {
      name: "Keep the cover of June 4, 2026 (2)",
    }),
  ).toBeChecked();
  // The radio is visually hidden behind its thumbnail label.
  await mergeDialog
    .locator(
      'label:has(input[aria-label="Keep the cover of June 4, 2026 (1)"])',
    )
    .click();
  await expect(keepOriginalCover).toBeChecked();
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

  // Narrow screens keep the drilled-in Moment pane, then go back to the
  // outline instead of a side sheet.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(outline).toHaveCount(0);
  const mobileAlex = desktopAccess.getByRole("checkbox", {
    name: "Allow Alex for this Moment",
  });
  await mobileAlex.uncheck();
  await desktopAccess.getByRole("button", { name: "Undo" }).click();
  await expect(mobileAlex).toBeChecked();
  await page.getByRole("link", { name: "Outline" }).click();
  await expect(outline).toBeVisible();
  await expect(page.getByRole("region", { name: /June 4, 2026/ })).toHaveCount(
    0,
  );
  // The merged Moment carries the plain date label, so its title gains the
  // weekday.
  await outline.getByRole("link", { name: "Thursday, June 4, 2026" }).click();
  await expect(
    page.getByRole("region", { name: /June 4, 2026/ }),
  ).toBeVisible();
  await expect(outline).toHaveCount(0);
});
