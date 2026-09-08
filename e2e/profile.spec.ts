import { expect, test } from "./fixtures";

test("a touch user clears their update email and can identify the current browser", async ({
  browser,
  baseURL,
}) => {
  const context = await browser.newContext({
    baseURL,
    hasTouch: true,
    viewport: { width: 390, height: 844 },
  });
  try {
    const page = await context.newPage();
    await page.goto("/setup");
    await page.getByRole("button", { name: "Claim installation" }).tap();
    await expect(
      page.getByRole("heading", { name: "Your albums" }),
    ).toBeVisible();
    await page.goto("/profile");
    const email = page.getByRole("combobox", { name: "Email for updates" });
    await expect(email).toHaveText("curator@example.test");
    await email.tap();
    await page.getByRole("option", { name: "No email selected" }).tap();
    await expect(email).toHaveText("No email selected");
    await page
      .getByRole("checkbox", { name: "Email me when there are updates" })
      .tap();
    await page.getByRole("button", { name: "Save profile" }).tap();
    await expect(page.getByRole("status")).toHaveText("Profile saved.");
    await page.reload();
    await expect(email).toHaveText("No email selected");
    await expect(
      page.getByRole("checkbox", { name: "Email me when there are updates" }),
    ).not.toBeChecked();
    const sessions = page.getByRole("table", { name: "Browser sessions" });
    await expect(
      sessions.getByText("This browser", { exact: true }),
    ).toBeVisible();
    await expect(
      sessions.getByRole("button", { name: "This browser" }),
    ).toHaveCount(0);
  } finally {
    await context.close();
  }
});
