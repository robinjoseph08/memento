import type { Page } from "@playwright/test";

import { expect, finishOnboarding, test } from "./fixtures";

async function signIn(page: Page, email: string, name = "Provider Name") {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}

async function createApprovedPerson(page: Page, name: string, email: string) {
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

test("a Curator invites and onboards a Person, resolves an unknown identity, and reviews an explicit Album request", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(120_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  // The first Curator onboards too, with an honest empty Album list.
  await expect(
    page.getByRole("heading", { name: "Welcome to Memento" }),
  ).toBeVisible();
  await expect(page.getByText(/Nothing is imported yet/)).toBeVisible();
  await finishOnboarding(page);
  await expect(page).toHaveURL(/\/curator$/);

  // Invitation: outreach through the controlled SMTP server, no token in the link.
  await createApprovedPerson(page, "Alex", "alex@example.test");
  await page
    .getByRole("button", { name: "Send invitation to alex@example.test" })
    .click();
  await expect(
    page.getByText(/^Invitation (queued|sending|sent)/),
  ).toBeVisible();
  await expect
    .poll(async () => (await immich.mail()).messages.length, {
      timeout: 20_000,
    })
    .toBe(1);
  const [message] = (await immich.mail()).messages;
  expect(message.to).toBe("alex@example.test");
  expect(message.data).toContain(`${baseURL}/sign-in`);
  expect(message.data).not.toMatch(/token=/);
  await expect(page.getByText(/^Invitation sent/)).toBeVisible({
    timeout: 15_000,
  });
  await page
    .getByRole("button", { name: "Send invitation to alex@example.test" })
    .waitFor({ state: "hidden" });

  const memberContext = await browser.newContext({ baseURL });
  const strangerContext = await browser.newContext({ baseURL });
  try {
    // The invited Person and a directly approved one share the same flow.
    const member = await memberContext.newPage();
    await signIn(member, "alex@example.test", "Alex");
    await expect(
      member.getByRole("heading", { name: "Welcome to Memento" }),
    ).toBeVisible();
    await expect(
      member.getByText(/Nothing is shared with you yet/),
    ).toBeVisible();
    await member
      .getByRole("textbox", { name: "Your name" })
      .fill("Alex Family");
    await finishOnboarding(member);
    await expect(
      member.getByRole("heading", { name: "No albums yet" }),
    ).toBeVisible();
    await member.goto("/welcome");
    await expect(member).toHaveURL(/\/albums$/);

    // Unknown identity: repeated sign-ins keep one request and one alert.
    const stranger = await strangerContext.newPage();
    for (let attempt = 0; attempt < 2; attempt++) {
      await signIn(stranger, "stranger@example.test", "Stranger");
      await expect(stranger.getByRole("alert")).toContainText(
        "asked to review your request",
      );
    }
    await page.reload();
    const requestsLink = page.getByRole("link", {
      name: /Requests 1 ?pending/,
    });
    await expect(requestsLink).toBeVisible();
    await requestsLink.click();
    const pending = page.getByRole("region", {
      name: /Waiting for a decision/,
    });
    await expect(
      pending.getByText("stranger@example.test", { exact: false }),
    ).toBeVisible();
    await expect(pending.getByText(/2 sign-ins/)).toBeVisible();
    await expect(pending.getByRole("listitem")).toHaveCount(1);

    // Deny suppresses further alerts; the next sign-in only refreshes the record.
    await page
      .getByRole("button", { name: "Deny request from stranger@example.test" })
      .click();
    const decided = page.getByRole("region", { name: /Decided/ });
    await expect(decided.getByText(/Denied by/)).toBeVisible();
    await signIn(stranger, "stranger@example.test", "Stranger");
    await expect(stranger.getByRole("alert")).toContainText(
      "asked to review your request",
    );
    await page.reload();
    await expect(
      page.getByRole("link", { name: /Requests \d+ ?pending/ }),
    ).toHaveCount(0);
    await expect(page.getByText(/3 sign-ins/)).toBeVisible();
    await expect(page.getByRole("listitem")).toHaveCount(1);

    // Reconsider, then approve by creating a Person; no Album access is granted.
    await page
      .getByRole("button", {
        name: "Reconsider request from stranger@example.test",
      })
      .click();
    await expect(
      page.getByRole("link", { name: /Requests 1 ?pending/ }),
    ).toBeVisible();
    await page
      .getByRole("button", {
        name: "Approve request from stranger@example.test",
      })
      .click();
    const dialog = page.getByRole("dialog");
    await expect(
      dialog.getByRole("textbox", { name: "Display name" }),
    ).toHaveValue("Stranger");
    await dialog
      .getByRole("textbox", { name: "Display name" })
      .fill("Sam Stranger");
    await dialog
      .getByRole("button", { name: "Approve and open person" })
      .click();
    await expect(
      page.getByRole("heading", { name: "Sam Stranger", exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("table", { name: "Preauthorizations", exact: true })
        .getByText("stranger@example.test", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", {
        name: "Send invitation to stranger@example.test",
      }),
    ).toBeVisible();
    await signIn(stranger, "stranger@example.test", "Stranger");
    await expect(
      stranger.getByRole("heading", { name: "Welcome to Memento" }),
    ).toBeVisible();
    await finishOnboarding(stranger);
    await expect(
      stranger.getByRole("heading", { name: "No albums yet" }),
    ).toBeVisible();

    // An existing Person visiting an inaccessible Album creates nothing until they ask.
    await page.goto("/curator/import?q=Coast");
    await page.getByRole("button", { name: "Import", exact: true }).click();
    await expect(
      page.getByRole("navigation", { name: "Album outline" }),
    ).toBeVisible({ timeout: 60_000 });
    const albumPath = new URL(page.url()).pathname.replace("/curator", "");
    await member.goto(`${albumPath}/photos`);
    await expect(
      member.getByRole("heading", { name: "Album not available" }),
    ).toBeVisible();
    await page.goto("/curator/requests");
    await expect(page.getByRole("listitem")).toHaveCount(1);
    await member.getByRole("button", { name: "Request access" }).click();
    await expect(member.getByRole("status")).toContainText(
      "asked to share this album with you",
    );
    await page.reload();
    const albumRequest = page
      .getByRole("region", { name: /Waiting for a decision/ })
      .getByRole("listitem");
    await expect(albumRequest).toHaveCount(1);
    await expect(albumRequest).toContainText("Alex Family");
    await expect(albumRequest).toContainText("Fixture Album - Coast");
    await page
      .getByRole("button", { name: "Approve request from Alex Family" })
      .click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Approve request from Alex Family" })
      .click();
    await expect(page).toHaveURL(new RegExp(`/curator${albumPath}$`));
    // Approval recorded the decision only: Alex still cannot see the Album.
    await member.reload();
    await expect(
      member.getByRole("heading", { name: "Album not available" }),
    ).toBeVisible();
    await page.goto("/curator/people");
    await expect(page.getByRole("link", { name: /Alex Family/ })).toHaveCount(
      1,
    );
  } finally {
    await memberContext.close();
    await strangerContext.close();
  }
});

test("an Invitation interrupted after possible SMTP acceptance stays uncertain until a Curator retries it deliberately", async ({
  page,
  immich,
}) => {
  test.setTimeout(120_000);
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await createApprovedPerson(page, "Alex", "alex@example.test");
  const personURL = page.url();
  await immich.mailMode("hold");
  await page
    .getByRole("button", { name: "Send invitation to alex@example.test" })
    .click();
  await expect
    .poll(async () => (await immich.mail()).held, { timeout: 20_000 })
    .toBe(1);
  // The process dies while the mail server holds the acceptance reply.
  await immich.restart();
  await page.goto(personURL);
  await expect(page.getByText("Invitation delivery uncertain")).toBeVisible({
    timeout: 30_000,
  });
  await expect(
    page.getByText(/may (already )?have (been delivered|accepted)/),
  ).toBeVisible();
  expect((await immich.mail()).messages).toHaveLength(1);
  // Nothing resends on its own after the restart.
  await page.reload();
  await expect(page.getByText("Invitation delivery uncertain")).toBeVisible();
  expect((await immich.mail()).messages).toHaveLength(1);

  await immich.mailMode("accept");
  await page
    .getByRole("button", { name: "Send invitation to alex@example.test again" })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("could deliver a duplicate");
  await dialog
    .getByRole("button", { name: "Send invitation to alex@example.test again" })
    .click();
  await expect(page.getByText(/^Invitation sent/)).toBeVisible({
    timeout: 20_000,
  });
  await expect.poll(async () => (await immich.mail()).messages.length).toBe(2);
});

test.describe("without SMTP", () => {
  test.use({ fixtureFlags: ["--no-smtp"] });

  test("Person setup and sign-in work while Invitations explain the missing mail server", async ({
    page,
    browser,
    baseURL,
    request,
  }) => {
    await page.goto("/setup");
    await page.getByRole("button", { name: "Claim installation" }).click();
    await finishOnboarding(page);
    await createApprovedPerson(page, "Alex", "alex@example.test");
    await page
      .getByRole("button", { name: "Send invitation to alex@example.test" })
      .click();
    await expect(page.getByRole("alert")).toContainText(
      "Email is not configured for this installation",
    );
    expect((await request.get("/health")).status()).toBe(200);
    const memberContext = await browser.newContext({ baseURL });
    try {
      const member = await memberContext.newPage();
      await signIn(member, "alex@example.test", "Alex");
      await finishOnboarding(member);
      await expect(
        member.getByRole("heading", { name: "No albums yet" }),
      ).toBeVisible();
    } finally {
      await memberContext.close();
    }
  });
});
