import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

async function signIn(page: Page, email: string) {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}
async function profile(page: Page) {
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Profile" }).click();
  await expect(
    page.getByRole("heading", { name: "Your profile" }),
  ).toBeVisible();
}

test("a Curator approves access, a member manages their profile and sessions, and deactivation ends access", async ({
  page,
  browser,
  baseURL,
}) => {
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person" }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill("Alex Family");
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(
    page.getByRole("heading", { name: "Alex Family" }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Google email address" })
    .fill("alex@example.test");
  await page.getByRole("button", { name: "Preauthorize email" }).click();
  await expect(
    page
      .getByRole("table", { name: "Preauthorizations", exact: true })
      .getByText("alex@example.test", { exact: true }),
  ).toBeVisible();

  const memberContext = await browser.newContext({ baseURL });
  const otherContext = await browser.newContext({ baseURL });
  try {
    const member = await memberContext.newPage();
    await signIn(member, "alex@example.test");
    await expect(
      member.getByRole("heading", { name: "No albums yet" }),
    ).toBeVisible();
    await expect(
      member.getByRole("link", { name: "People", exact: true }),
    ).toHaveCount(0);
    await profile(member);
    await expect(
      member.getByRole("combobox", { name: "Email for updates" }),
    ).toHaveValue("alex@example.test");
    await expect(
      member.getByRole("checkbox", { name: "Email me when there are updates" }),
    ).toBeChecked();
    await expect(
      member.getByRole("columnheader", { name: "Expires", exact: true }),
    ).toHaveCount(0);
    await expect(
      member.getByRole("button", { name: "Unlink alex@example.test" }),
    ).toBeDisabled();
    await member
      .getByRole("textbox", { name: "Display name" })
      .fill("Alex Updated");
    await member
      .getByRole("combobox", { name: "Email for updates" })
      .selectOption("alex@example.test");
    await member
      .getByRole("checkbox", { name: "Email me when there are updates" })
      .check();
    await member.getByRole("button", { name: "Save profile" }).click();
    await expect(member.getByRole("status")).toHaveText("Profile saved.");
    await expect(
      member.getByText("This browser", { exact: true }),
    ).toBeVisible();

    await page
      .getByRole("textbox", { name: "Google email address" })
      .fill("alex.other@example.test");
    await page.getByRole("button", { name: "Preauthorize email" }).click();
    const other = await otherContext.newPage();
    await signIn(other, "alex.other@example.test");
    await profile(other);
    await expect(
      other.getByRole("textbox", { name: "Display name" }),
    ).toHaveValue("Alex Updated");
    await expect(
      other.getByRole("table", { name: "Browser sessions" }).getByRole("row"),
    ).toHaveCount(3);
    await other.getByRole("button", { name: "Sign out everywhere" }).click();
    await other
      .getByRole("dialog")
      .getByRole("button", { name: "Sign out everywhere" })
      .click();
    await expect(
      other.getByRole("heading", { name: "Welcome back" }),
    ).toBeVisible();
    await member.reload();
    await expect(
      member.getByRole("heading", { name: "Welcome back" }),
    ).toBeVisible();

    await signIn(member, "alex@example.test");
    await expect(
      member.getByRole("heading", { name: "No albums yet" }),
    ).toBeVisible();
    await page
      .getByRole("checkbox", { name: "Deactivate this person" })
      .check();
    await page.getByRole("button", { name: "Save person" }).click();
    await expect(page.getByRole("status")).toHaveText("Person saved.");
    await member.reload();
    await expect(
      member.getByRole("heading", { name: "Welcome back" }),
    ).toBeVisible();
    await signIn(member, "alex@example.test");
    await expect(member.getByRole("alert")).toBeVisible();

    await page
      .getByRole("checkbox", { name: "Deactivate this person" })
      .uncheck();
    await page.getByRole("button", { name: "Save person" }).click();
    await expect(page.getByRole("status")).toHaveText("Person saved.");

    let started!: () => void;
    let release!: () => void;
    const requestStarted = new Promise<void>((resolve) => {
      started = resolve;
    });
    const released = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route(
      "**/api/identity/sessions",
      async (route) => {
        const response = await route.fetch();
        started();
        await released;
        await route.fulfill({ response }).catch(() => {});
      },
      { times: 1 },
    );
    await profile(page);
    await expect(
      page.getByRole("textbox", { name: "Display name" }),
    ).toHaveValue("Local Curator");
    await requestStarted;
    try {
      await page.getByRole("button", { name: "Account menu" }).click();
      await page
        .getByRole("menuitem", { name: "Sign out", exact: true })
        .click();
      await expect(
        page.getByRole("heading", { name: "Welcome back" }),
      ).toBeVisible();
      await page
        .getByRole("textbox", { name: "Email", exact: true })
        .fill("alex@example.test");
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
      await profile(page);
      await expect(
        page.getByRole("textbox", { name: "Display name" }),
      ).toHaveValue("Alex Updated");
      await expect(
        page.getByText("curator@example.test", { exact: true }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("link", { name: "People", exact: true }),
      ).toHaveCount(0);
    } finally {
      release();
    }
    await expect(
      page.getByRole("table", { name: "Browser sessions" }).getByRole("row"),
    ).toHaveCount(2);
    await expect(
      page.getByText("curator@example.test", { exact: true }),
    ).toHaveCount(0);
  } finally {
    await memberContext.close();
    await otherContext.close();
  }
});
