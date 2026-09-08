import { expect, test } from "./fixtures";

test("mobile navigation uses a dismissible drawer and search keeps keyboard focus", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  const trigger = page.getByRole("button", { name: "Open navigation" });
  await trigger.click();
  const drawer = page.getByRole("dialog", { name: "Navigation" });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText("memento", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(trigger).toBeFocused();
  await trigger.click();
  await drawer.getByRole("link", { name: "People", exact: true }).click();
  await expect(drawer).toBeHidden();
  const search = page.getByRole("searchbox", { name: "Search people" });
  await search.fill("Curator");
  await search.press("Enter");
  await expect(search).toBeFocused();
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(search).toBeFocused();
  await page.getByRole("link", { name: /Local Curator/ }).click();
  await expect(
    page.getByRole("checkbox", { name: "Curator", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("checkbox", { name: "Deactivate this person" }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Unlink curator@example.test" }),
  ).toBeDisabled();
  await expect(
    page
      .getByRole("region", { name: "Browser sessions" })
      .getByText("Expires:", { exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
});
