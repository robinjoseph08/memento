import type { Locator, Page } from "@playwright/test";

import { expect, finishOnboarding, test } from "./fixtures";

async function createPerson(page: Page, name: string, email: string) {
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await page.getByRole("textbox", { name: "Google email address" }).fill(email);
  await page.getByRole("button", { name: "Preauthorize email" }).click();
  await expect(
    page
      .getByRole("table", { name: "Preauthorizations", exact: true })
      .getByText(email, { exact: true }),
  ).toBeVisible();
}

async function signInAndOnboard(page: Page, email: string, name: string) {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await finishOnboarding(page);
  await expect(
    page.getByRole("heading", { name: "No albums yet", exact: true }),
  ).toBeVisible();
}

// Imports one fixture Album, grants Album access to everyone named, and
// publishes it. Returns the viewer path of the Album.
async function importAndPublish(page: Page, title: string, people: string[]) {
  await page.goto(`/curator/import?q=${encodeURIComponent(title)}`);
  await page
    .getByRole("article")
    .filter({ has: page.getByRole("heading", { name: title, exact: true }) })
    .getByRole("button", { name: "Import", exact: true })
    .click();
  const outline = page.getByRole("navigation", { name: "Album outline" });
  await expect(
    outline.getByRole("link", { name: "Album access", exact: true }),
  ).toBeVisible({ timeout: 60_000 });
  const viewerPath = new URL(page.url()).pathname.replace("/curator", "");
  await outline
    .getByRole("link", { name: "Album access", exact: true })
    .click();
  const albumAccess = page.getByRole("form", {
    name: "Album access",
    exact: true,
  });
  await albumAccess.getByText(/not seen in this album/).click();
  for (const person of people) {
    await albumAccess
      .getByRole("checkbox", { name: `Album access for ${person}` })
      .check();
  }
  await albumAccess
    .getByRole("button", { name: "Save Album access", exact: true })
    .click();
  await expect(albumAccess.getByRole("status")).toHaveText(
    "Album access saved.",
  );
  await page
    .getByRole("button", { name: "Review & publish", exact: true })
    .click();
  const publication = page.getByRole("dialog", {
    name: "Ready to publish?",
    exact: true,
  });
  await publication
    .getByRole("button", { name: "Publish album", exact: true })
    .click();
  await expect(publication).toHaveCount(0);
  return viewerPath;
}

function bell(page: Page, unread: number) {
  return page.getByRole("button", {
    name: unread === 0 ? "Updates" : `Updates, ${unread} unread`,
    exact: true,
  });
}

async function openUpdates(page: Page, unread: number): Promise<Locator> {
  await bell(page, unread).click();
  const panel = page.getByRole("dialog", { name: "New updates", exact: true });
  await expect(panel).toBeVisible();
  return panel;
}

test("Curator previews a mixed batch, excludes one update, adds a note, approves twice, and members read updates independently", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(180_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await createPerson(page, "Alex", "alex@example.test");
  await createPerson(page, "Sam", "sam@example.test");

  const alexContext = await browser.newContext({ baseURL });
  const samContext = await browser.newContext({ baseURL });
  try {
    // Both members finish Onboarding before anything is published, so their
    // baselines are empty and both Albums are genuinely new to them.
    const alex = await alexContext.newPage();
    const sam = await samContext.newPage();
    await signInAndOnboard(alex, "alex@example.test", "Alex");
    await signInAndOnboard(sam, "sam@example.test", "Sam");
    await expect(bell(alex, 0)).toBeVisible();

    const coastPath = await importAndPublish(page, "Fixture Album - Coast", [
      "Alex",
      "Sam",
    ]);
    await importAndPublish(page, "Fixture Album - Family", ["Alex", "Sam"]);

    // Nothing was sent by publishing: the bell is still quiet.
    await alex.reload();
    await expect(bell(alex, 0)).toBeVisible();

    await page.getByRole("link", { name: "Updates", exact: true }).click();
    await expect(page).toHaveURL(/\/curator\/updates$/);
    const form = page.getByRole("form", { name: "Send updates", exact: true });
    const alexRow = form.getByRole("listitem").filter({ hasText: "Alex" });
    const samRow = form.getByRole("listitem").filter({ hasText: "Sam" });
    await expect(form.getByRole("listitem")).toHaveCount(2);
    await expect(alexRow).toContainText("Email to alex@example.test");
    await expect(alexRow).toContainText("2 albums");
    await expect(alexRow).toContainText("6 photos, 3 videos");
    await expect(samRow).toContainText("2 albums");

    // A second Curator tab captures the same preview independently.
    const second = await page.context().newPage();
    await second.goto("/curator/updates");
    const secondForm = second.getByRole("form", {
      name: "Send updates",
      exact: true,
    });
    await expect(secondForm.getByRole("listitem")).toHaveCount(2);

    await alexRow
      .getByRole("button", { name: "Show albums for Alex", exact: true })
      .click();
    await expect(alexRow).toContainText(
      "Fixture Album - Coast · New album · 4 photos, 2 videos",
    );
    await alexRow
      .getByRole("checkbox", {
        name: "Include Fixture Album - Family for Alex",
        exact: true,
      })
      .uncheck();
    await expect(alexRow).toContainText("1 album (1 left out)");
    await expect(alexRow).toContainText("4 photos, 2 videos");
    await page
      .getByRole("textbox", { name: "Note for everyone (optional)" })
      .fill("Enjoy the new photos!");
    await page
      .getByRole("button", { name: "Send updates to 2 people", exact: true })
      .click();
    const result = page.getByRole("region", {
      name: "Updates sent to 2 people",
      exact: true,
    });
    await expect(result).toBeVisible();
    await expect(result).toContainText("Alex · 1 album, 4 photos, 2 videos");
    await expect(result).toContainText("Sam · 2 albums, 6 photos, 3 videos");

    // Approving the same content again from the other tab sends nothing.
    await second
      .getByRole("button", { name: "Send updates to 2 people", exact: true })
      .click();
    const repeat = second.getByRole("region", {
      name: "No updates were sent",
      exact: true,
    });
    await expect(repeat).toBeVisible();
    await expect(repeat).toContainText("Alex · Not sent.");
    await expect(repeat).toContainText("Sam · Not sent.");
    await second.close();
    // Only the Album left out of Alex's update is still waiting.
    await page
      .getByRole("button", { name: "Check again", exact: true })
      .click();
    await expect(form.getByRole("listitem")).toHaveCount(1);
    await expect(alexRow).toContainText("1 album");
    await expect(alexRow).toContainText("2 photos, 1 video");

    // Alex reads the one-Album update straight into the Album.
    await alex.reload();
    const alexUpdates = await openUpdates(alex, 1);
    const alexItem = alexUpdates.getByRole("listitem");
    await expect(alexItem).toHaveCount(1);
    await expect(alexItem).toContainText("Fixture Album - Coast");
    await expect(alexItem).toContainText("4 photos and 2 videos · New album");
    await expect(alexItem).toContainText("Enjoy the new photos!");
    await expect(alexItem).toContainText(/[A-Z][a-z]+ \d{1,2}, \d{4}/);
    await expect(alexItem).not.toContainText(/\d:\d\d/);
    await alexItem
      .getByRole("button", { name: "Open Fixture Album - Coast", exact: true })
      .click();
    await expect(alex).toHaveURL(new RegExp(`${coastPath}/photos$`));
    await expect(bell(alex, 0)).toBeVisible();

    // Sam browses the same Album directly, which consumes nothing.
    await sam.goto(`${coastPath}/photos`);
    await expect(
      sam.getByRole("navigation", { name: "Album media" }),
    ).toBeVisible();
    await expect(bell(sam, 1)).toBeVisible();
    const samUpdates = await openUpdates(sam, 1);
    const samItem = samUpdates.getByRole("listitem");
    await expect(samItem).toContainText("6 photos and 3 videos");
    await samItem
      .getByRole("button", {
        name: "Open 2 albums: Fixture Album - Coast, Fixture Album - Family",
        exact: true,
      })
      .click();
    await expect(sam).toHaveURL(/\/albums$/);
    await expect(bell(sam, 0)).toBeVisible();
    const caughtUp = await openUpdates(sam, 0);
    await expect(
      caughtUp.getByRole("button", { name: "All caught up", exact: true }),
    ).toBeDisabled();
    await expect(caughtUp.getByRole("listitem")).toHaveCount(0);
    await sam.keyboard.press("Escape");

    // Rename and then permanently delete the announced Album. The summary
    // keeps the reviewed title, and its destination fails neutrally.
    await page.goto(`/curator${coastPath}`);
    const outline = page.getByRole("navigation", { name: "Album outline" });
    await outline
      .getByRole("link", { name: "Album details", exact: true })
      .click();
    await page.getByRole("textbox", { name: "Album title" }).fill("Renamed");
    await page
      .getByRole("form", { name: "Edit album title", exact: true })
      .getByRole("button", { name: /Save/ })
      .click();
    await expect(
      outline.getByRole("link", { name: "Album details", exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Delete Album", exact: true })
      .click();
    const deletion = page.getByRole("dialog", {
      name: "Permanently delete this Album?",
      exact: true,
    });
    await deletion
      .getByRole("textbox", { name: "Type the Album title" })
      .fill("Renamed");
    await deletion
      .getByRole("button", { name: "Permanently delete Album", exact: true })
      .click();
    await expect(page).toHaveURL(/\/curator$/);

    // The bell holds only new updates; the read one is on the Updates page.
    await alex.goto("/albums");
    const caughtUpAlex = await openUpdates(alex, 0);
    await expect(caughtUpAlex.getByRole("listitem")).toHaveCount(0);
    await caughtUpAlex
      .getByRole("link", { name: "See all updates", exact: true })
      .click();
    await expect(alex).toHaveURL(/\/notifications$/);
    const history = alex.getByRole("list");
    await expect(history.getByRole("listitem")).toContainText(
      "Fixture Album - Coast",
    );
    await history
      .getByRole("button", { name: "Open Fixture Album - Coast", exact: true })
      .click();
    await expect(
      alex.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();
  } finally {
    await alexContext.close();
    await samContext.close();
  }
});
