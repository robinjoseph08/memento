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
  await search.fill("unsubmitted draft");
  await page.getByRole("button", { name: "Clear search" }).click();
  await expect(search).toHaveValue("");
  await expect(search).toBeFocused();
  await expect(page).toHaveURL(/\/curator\/people$/);
  await expect(page.getByRole("button", { name: "Clear search" })).toBeHidden();
  await page.goBack();
  await expect(search).toHaveValue("Curator");
  await page.goForward();
  await expect(search).toHaveValue("");
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
  await page.getByRole("link", { name: "All people" }).click();
  await expect(
    page.getByRole("heading", { name: "People", exact: true }),
  ).toBeVisible();
});

test("Immich search keeps focus, clears immediately, and follows browser history", async ({
  page,
  immich,
}) => {
  await immich.online();
  await page.route("**/api/media/sources/**/cover", (route) => {
    const portrait = route.request().url().includes("coast");
    return route.fulfill({
      contentType: "image/svg+xml",
      body: `<svg xmlns="http://www.w3.org/2000/svg" width="${portrait ? 100 : 800}" height="${portrait ? 400 : 100}"><rect width="100%" height="100%" fill="#06b6d4"/></svg>`,
    });
  });
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await page.getByRole("link", { name: "Import an album" }).click();
  await page.getByRole("link", { name: "Next page" }).click();
  await expect(page.getByText("Page 2 of 2", { exact: true })).toBeVisible();
  const search = page.getByRole("searchbox", { name: "Search Immich albums" });
  await search.fill("Fixture Album");
  await search.press("Enter");
  await expect(search).toBeFocused();
  await expect(page).toHaveURL(/q=Fixture\+Album&page=1$/);
  await expect(page.getByRole("article")).toHaveCount(2);
  for (const cover of await page.getByRole("article").getByRole("img").all()) {
    await expect
      .poll(() =>
        cover.evaluate(
          (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
        ),
      )
      .toBe(true);
    const size = await cover.evaluate((image: HTMLImageElement) => ({
      width: image.getBoundingClientRect().width,
      height: image.getBoundingClientRect().height,
      ratio: image.naturalWidth / image.naturalHeight,
    }));
    expect(size.width / size.height).toBeCloseTo(size.ratio, 2);
    expect(size.height).toBeLessThanOrEqual(240);
  }
  await search.fill("Coast");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(search).toBeFocused();
  await expect(page.getByRole("article")).toHaveCount(1);
  await search.fill("unsubmitted draft");
  await page.goBack();
  await expect(search).toHaveValue("Fixture Album");
  await expect(page.getByRole("article")).toHaveCount(2);
  await page.goForward();
  await expect(search).toHaveValue("Coast");
  await search.fill("another draft");
  await page.getByRole("button", { name: "Clear search" }).click();
  await expect(search).toHaveValue("");
  await expect(search).toBeFocused();
  await expect(page).toHaveURL(/q=&page=1$/);
  await expect(page.getByRole("button", { name: "Clear search" })).toBeHidden();
  await expect(page.getByText("Page 1 of 2", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "All albums" }).click();
  await expect(page).toHaveURL(/\/curator$/);
  await expect(
    page.getByRole("heading", { name: "Your albums", exact: true }),
  ).toBeVisible();
});
