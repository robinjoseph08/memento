import { expect, test } from "./fixtures";

test("claims during an Immich outage, recovers, and revokes the signed-out session", async ({
  page,
  context,
  request,
  baseURL,
  fixtureURL,
}) => {
  await page.goto("/setup");
  await expect(page.getByText(/first successful sign-in/i)).toBeVisible();
  await expect(page.getByRole("button", { name: "Check again" })).toBeVisible();
  expect((await request.get("/health")).status()).toBe(200);
  await expect(page.getByLabel("Subject", { exact: true })).toHaveCount(0);
  await page.getByLabel("Email", { exact: true }).fill("Curator@Example.com");
  await page.getByLabel("Display name").fill("Fixture Curator");
  await page.getByLabel("Display name").press("Enter");
  await expect(page).toHaveURL(/\/curator$/);
  await expect(
    page.getByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();

  let session = (await context.cookies()).find(
    (cookie) => cookie.name === "memento_session",
  );
  expect(session).toBeDefined();
  expect(session?.httpOnly).toBe(true);
  expect(session?.sameSite).toBe("Lax");

  const account = page.getByRole("button", { name: "Account menu" });
  const openConnection = async () => {
    await account.click();
    await page.getByRole("menuitem", { name: "Immich connection" }).click();
    await expect(
      page.getByRole("dialog", { name: "Immich connection" }),
    ).toBeVisible();
  };
  await expect(
    page.getByRole("heading", { name: "Immich connection" }),
  ).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await account.click();
  const accountTrigger = page.locator('button[aria-label="Account menu"]');
  await expect(accountTrigger).toHaveCSS("pointer-events", "auto");
  await expect(accountTrigger).toHaveCSS("cursor", "pointer");
  const signOutItem = page.getByRole("menuitem", { name: "Sign out" });
  await signOutItem.hover();
  await expect(signOutItem).toHaveCSS("cursor", "pointer");
  await expect(signOutItem).toHaveCSS("box-shadow", "none");
  await expect(signOutItem).toHaveCSS("outline-style", "none");
  const darkMode = page.getByRole("menuitemcheckbox", { name: "Dark mode" });
  await expect(darkMode).toHaveCSS("cursor", "pointer");
  const unhighlightedBackground = await darkMode.evaluate(
    (element) => getComputedStyle(element).backgroundColor,
  );
  await darkMode.click();
  await expect(darkMode).not.toBeChecked();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await darkMode.press("Space");
  await expect(darkMode).toBeChecked();
  await expect(darkMode).toBeFocused();
  await expect(darkMode).not.toHaveCSS(
    "background-color",
    unhighlightedBackground,
  );
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await account.click();
  await expect(page.getByRole("menu")).toHaveCount(0);
  await account.click();
  await expect(
    page.getByText("Fixture Curator", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Curator", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(account).toBeFocused();
  await openConnection();
  await expect(page.getByText(/Immich is unavailable/)).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await expect(account).toBeFocused();
  await page.setViewportSize({ width: 1280, height: 720 });

  expect(
    (
      await request.post(`${fixtureURL}/__fixture/state`, {
        data: { available: true },
      })
    ).status(),
  ).toBe(200);
  await openConnection();
  await page.getByRole("button", { name: "Check again" }).click();
  await expect(page.getByText(/2\.7\.0/)).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();

  expect((await request.post(`${fixtureURL}/__fixture/restart`)).status()).toBe(
    204,
  );
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  // A diagnostic started under an old session must not replace a newer cookie.
  let reportStarted!: () => void;
  let release!: () => void;
  let reportFinished!: () => void;
  const started = new Promise<void>((resolve) => {
    reportStarted = resolve;
  });
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  const finished = new Promise<void>((resolve) => {
    reportFinished = resolve;
  });
  await page.route(
    "**/api/curator/connection",
    async (route) => {
      const response = await route.fetch();
      reportStarted();
      await released;
      // Cancellation can close the request before the held response is released.
      await route.fulfill({ response }).catch(() => {});
      reportFinished();
    },
    { times: 1 },
  );
  await openConnection();
  await started;
  await page.getByRole("button", { name: "Close dialog" }).click();
  await account.click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.getByLabel("Email", { exact: true }).fill("curator@example.com");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).toHaveURL(/\/curator$/);
  session = (await context.cookies()).find(
    (cookie) => cookie.name === "memento_session",
  );
  expect(session).toBeDefined();
  release();
  await finished;
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "No albums yet" }),
  ).toBeVisible();
  expect(
    (await context.cookies()).find(
      (cookie) => cookie.name === "memento_session",
    )?.value,
  ).toBe(session?.value);

  await account.click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);

  // Replaying the original cookie checks server-side revocation, not just removal.
  const revoked = await request.get(`${baseURL}/api/identity/me`, {
    headers: { Cookie: `memento_session=${session?.value}` },
  });
  expect(revoked.status()).toBe(401);
  await page.goto("/curator");
  await expect(page).toHaveURL(/\/sign-in$/);
});
