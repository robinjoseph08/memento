import type { Page } from "@playwright/test";

import { expect, finishOnboarding, test, type MailState } from "./fixtures";

// codeFor reads the newest Sign-in Code the fixture's SMTP server accepted
// for an address.
async function codeFor(mail: () => Promise<MailState>, email: string) {
  let message = "";
  await expect
    .poll(async () => {
      const { messages } = await mail();
      message = messages.filter((sent) => sent.to === email).at(-1)?.data ?? "";
      return message;
    })
    .toMatch(/Subject: \d{6} is your Memento sign-in code/);
  const code = /Subject: (\d{6})/.exec(message)?.[1] ?? "";
  // The code is in the body too, for anyone who opens the email.
  expect(message).toContain(`\r\n${code}\r\n`);
  return code;
}

async function claim(page: Page) {
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await expect(page).toHaveURL(/\/curator$/);
}

async function requestCode(page: Page, email: string) {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email address" }).fill(email);
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText(`We sent a code to ${email}`)).toBeVisible();
}

test("a preauthorized Person signs in with the code emailed to them", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  await claim(page);
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill("Alex");
  await page.getByRole("button", { name: "Create person" }).click();
  await page
    .getByRole("textbox", { name: "Email address" })
    .fill("alex@example.test");
  await page.getByRole("button", { name: "Preauthorize email" }).click();
  await expect(
    page
      .getByRole("table", { name: "Preauthorizations", exact: true })
      .getByText("alex@example.test", { exact: true }),
  ).toBeVisible();

  const context = await browser.newContext({ baseURL });
  try {
    const member = await context.newPage();
    await requestCode(member, "alex@example.test");
    const code = await codeFor(() => immich.mail(), "alex@example.test");
    // Filling six digits signs in without another click.
    await member
      .getByRole("textbox", { name: "Sign-in code" })
      .fill(`${code.slice(0, 3)} ${code.slice(3)}`);
    await expect(
      member.getByRole("heading", { name: "Welcome to Memento" }),
    ).toBeVisible();
    await finishOnboarding(member);
    await expect(member).toHaveURL(/\/albums$/);
  } finally {
    await context.close();
  }
});

test("an unknown address verified by a code asks the Curator for access", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  await claim(page);

  const context = await browser.newContext({ baseURL });
  try {
    const stranger = await context.newPage();
    await requestCode(stranger, "jordan@example.test");
    const code = await codeFor(() => immich.mail(), "jordan@example.test");
    await stranger.getByRole("textbox", { name: "Sign-in code" }).fill(code);
    await expect(
      stranger.getByRole("heading", { name: "Request access" }),
    ).toBeVisible();
    await stranger.getByRole("textbox", { name: "Your name" }).fill("Jordan");
    await stranger.getByRole("button", { name: "Request" }).click();
    await expect(stranger.getByRole("status")).toHaveText(
      "Your Curator has been asked to review your request.",
    );
  } finally {
    await context.close();
  }

  await page.reload();
  await page.getByRole("link", { name: /Requests 1 ?pending/ }).click();
  const pending = page.getByRole("region", { name: /Waiting for a decision/ });
  await expect(pending.getByText("Jordan", { exact: true })).toBeVisible();
  await expect(pending.getByText("jordan@example.test")).toBeVisible();
  await expect(pending.getByText(/1 sign-in/)).toBeVisible();
});
