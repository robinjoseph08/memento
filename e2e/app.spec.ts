import { expect, test } from "@playwright/test";

test("shows the application introduction", async ({ page }) => {
  await page.goto("/");

  await expect(
    page.getByRole("heading", {
      level: 1,
      name: "Your application starts here.",
    }),
  ).toBeVisible();
});
