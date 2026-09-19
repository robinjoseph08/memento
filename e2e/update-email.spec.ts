import type { Page } from "@playwright/test";

import { expect, finishOnboarding, test } from "./fixtures";

// Creates a Person with a preauthorized email and returns their Curator page URL.
async function createPerson(page: Page, name: string, email: string) {
  await page.goto("/curator/people");
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
  return page.url();
}

// Signs in and completes Onboarding, optionally asking for update email.
async function signInAndOnboard(
  page: Page,
  email: string,
  name: string,
  subscribe: boolean,
) {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome to Memento" }),
  ).toBeVisible();
  // Onboarding preselects the sign-in email with updates on; a Person who
  // wants no update email clears the destination.
  await page.getByRole("combobox", { name: "Email for updates" }).click();
  await page
    .getByRole("option", {
      name: subscribe ? email : "No email selected",
      exact: true,
    })
    .click();
  const updates = page.getByRole("checkbox", {
    name: "Email me when there are updates",
  });
  if (subscribe) await updates.check();
  else await updates.uncheck();
  await finishOnboarding(page);
  await expect(
    page.getByRole("heading", { name: "No albums yet", exact: true }),
  ).toBeVisible();
}

// Imports one fixture Album and returns its viewer path. Publishing is the
// caller's choice so the dashboard can show an Album that is not ready.
async function importAlbum(page: Page, title: string) {
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
  return new URL(page.url()).pathname.replace("/curator", "");
}

async function grantAndPublish(page: Page, people: string[]) {
  const outline = page.getByRole("navigation", { name: "Album outline" });
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
}

test("update email is sent, retried deliberately after failure and uncertainty, unsubscribed through the confirmation page, and the dashboard sorts the work", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(300_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await expect(page).toHaveURL(/\/curator$/);
  const attention = page.getByRole("region", { name: "Needs attention" });
  const ready = page.getByRole("region", { name: "Ready when you are" });
  await expect(attention).toContainText("Nothing needs your attention");
  await expect(ready).toContainText("Everything is published");

  const alexURL = await createPerson(page, "Alex", "alex@example.test");
  await createPerson(page, "Sam", "sam@example.test");
  const alexContext = await browser.newContext({ baseURL });
  const samContext = await browser.newContext({ baseURL });
  const visitorContext = await browser.newContext({ baseURL });
  try {
    // Alex asks for email; Sam stays in app only.
    const alex = await alexContext.newPage();
    const sam = await samContext.newPage();
    await signInAndOnboard(alex, "alex@example.test", "Alex", true);
    await signInAndOnboard(sam, "sam@example.test", "Sam", false);

    const coastPath = await importAlbum(page, "Fixture Album - Coast");
    const coastID = coastPath.split("/").pop() ?? "";
    await grantAndPublish(page, ["Alex", "Sam"]);
    // The Family import stays unpublished so the dashboard can show waiting work.
    await importAlbum(page, "Fixture Album - Family");
    await page.goto("/curator");
    await expect(ready).toContainText(
      "2 people have new photos or videos to hear about",
    );
    await expect(ready).toContainText("Fixture Album - Family");
    await expect(ready).toContainText("Nobody has access yet");
    await expect(
      ready.getByRole("link", { name: "Set up access" }),
    ).toBeVisible();
    await expect(ready).not.toContainText(/fail/i);
    await expect(attention).toContainText("Nothing needs your attention");
    // The Albums page tells the same states apart.
    await page.goto("/curator/albums");
    await expect(page.locator('[data-album-state="published"]')).toContainText(
      "Fixture Album - Coast",
    );
    await expect(
      page.locator('[data-album-state="unpublished"]'),
    ).toContainText("Fixture Album - Family");

    // A mixed batch: the mail server rejects Alex's email for good, and Sam
    // never had one coming.
    await immich.mailMode("permanent");
    await page.goto("/curator/updates");
    const form = page.getByRole("form", { name: "Send updates", exact: true });
    await expect(form.getByRole("listitem")).toHaveCount(2);
    await expect(
      form.getByRole("listitem").filter({ hasText: "Alex" }),
    ).toContainText("Email to alex@example.test");
    await expect(
      form.getByRole("listitem").filter({ hasText: "Sam" }),
    ).toContainText("In app only, no email selected");
    await page
      .getByRole("textbox", { name: "Note for everyone (optional)" })
      .fill("Enjoy the coast!");
    await page
      .getByRole("button", { name: "Send updates to 2 people", exact: true })
      .click();
    const result = page.getByRole("region", {
      name: "Updates sent to 2 people",
      exact: true,
    });
    await expect(result).toBeVisible();
    const alexResult = result.getByRole("listitem").filter({ hasText: "Alex" });
    const samResult = result.getByRole("listitem").filter({ hasText: "Sam" });
    await expect(samResult).toContainText("In app only");
    // Progress is watched here until the email settles.
    await expect(alexResult).toContainText(
      "Email not delivered · alex@example.test",
      { timeout: 20_000 },
    );
    await expect(alexResult).toContainText("replied 550");
    expect((await immich.mail()).messages).toHaveLength(0);
    // Both members still got the update in app.
    await sam.reload();
    await expect(
      sam.getByRole("button", { name: "Updates, 1 unread", exact: true }),
    ).toBeVisible();

    // The failure is work on the dashboard. Retrying while the server holds
    // its answer, then restarting, leaves delivery uncertain.
    await page.goto("/curator");
    await expect(attention).toContainText("Update email for Alex");
    await expect(attention).toContainText("Email not delivered");
    await expect(ready).toContainText("Fixture Album - Family", {
      timeout: 10_000,
    });
    await expect(ready).not.toContainText("people have new photos");
    await immich.mailMode("hold");
    await attention
      .getByRole("button", { name: "Send email to alex@example.test again" })
      .click();
    await expect
      .poll(async () => (await immich.mail()).held, { timeout: 20_000 })
      .toBe(1);
    await immich.restart();
    await page.reload();
    await expect(attention).toContainText("Email delivery uncertain", {
      timeout: 30_000,
    });
    await expect(attention).toContainText(
      /may (already )?have (been delivered|accepted)/,
    );
    expect((await immich.mail()).messages).toHaveLength(1);
    // Nothing resends on its own after the restart.
    await page.reload();
    await expect(attention).toContainText("Email delivery uncertain");
    expect((await immich.mail()).messages).toHaveLength(1);

    // Only a deliberate retry, warned about duplicates, sends again.
    await immich.mailMode("accept");
    await attention
      .getByRole("button", { name: "Send email to alex@example.test again" })
      .click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("could deliver a duplicate");
    await dialog
      .getByRole("button", { name: "Send email to alex@example.test again" })
      .click();
    await expect
      .poll(async () => (await immich.mail()).messages.length, {
        timeout: 20_000,
      })
      .toBe(2);
    await expect(attention).toContainText("Nothing needs your attention", {
      timeout: 20_000,
    });
    const [, message] = (await immich.mail()).messages;
    expect(message.to).toBe("alex@example.test");
    expect(message.data).toContain("Fixture Album - Coast (new album)");
    expect(message.data).toContain("4 photos and 2 videos");
    expect(message.data).toContain("Enjoy the coast!");
    expect(message.data).toContain(`${baseURL}/albums/${coastID}/photos`);
    expect(message.data).not.toContain("coast-01");
    const link = /\/unsubscribe\?token=([A-Za-z0-9_-]+)/.exec(message.data);
    expect(link).not.toBeNull();
    const token = link?.[1] ?? "";

    // A scanner-like GET of the private link changes nothing.
    const scan = await visitorContext.request.get(
      `/unsubscribe?token=${token}`,
    );
    expect(scan.status()).toBe(200);
    expect(
      (
        await visitorContext.request.get(`/api/unsubscribe?token=${token}`)
      ).status(),
    ).toBe(200);
    await page.goto(alexURL);
    await expect(page.getByText("Subscribed", { exact: true })).toBeVisible();

    // Confirming on the page, without signing in, stops update email only.
    const visitor = await visitorContext.newPage();
    await visitor.goto(`/unsubscribe?token=${token}`);
    await expect(
      visitor.getByRole("heading", { name: "Stop update emails?" }),
    ).toBeVisible();
    await expect(visitor.getByText("alex@example.test")).toBeVisible();
    await visitor.getByRole("button", { name: "Stop update emails" }).click();
    await expect(
      visitor.getByRole("heading", { name: "Update emails are off" }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByText("Not subscribed", { exact: true }),
    ).toBeVisible();
    // The destination stays selected, so Invitations and account email still work.
    await expect(
      page.getByRole("definition").filter({ hasText: "alex@example.test" }),
    ).toBeVisible();
    await alex.reload();
    await expect(
      alex.getByRole("button", { name: "Updates, 1 unread", exact: true }),
    ).toBeVisible();
  } finally {
    await alexContext.close();
    await samContext.close();
    await visitorContext.close();
  }
});
