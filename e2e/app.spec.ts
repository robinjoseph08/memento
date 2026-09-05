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
  await page.getByLabel("Subject", { exact: true }).fill("fixture-curator");
  await page.getByLabel("Email", { exact: true }).fill("curator@example.com");
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

  expect(
    (
      await request.post(`${fixtureURL}/__fixture/state`, {
        data: { available: true },
      })
    ).status(),
  ).toBe(200);
  await page.getByRole("button", { name: "Check again" }).click();
  await expect(page.getByText(/2\.7\.0/)).toBeVisible();

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
  await page.getByRole("button", { name: "Check again" }).click();
  await started;
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.getByLabel("Subject", { exact: true }).fill("fixture-curator");
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

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);

  // Replaying the original cookie checks server-side revocation, not just removal.
  const revoked = await request.get(`${baseURL}/api/identity/me`, {
    headers: { Cookie: `memento_session=${session?.value}` },
  });
  expect(revoked.status()).toBe(401);
  await page.goto("/curator");
  await expect(page).toHaveURL(/\/sign-in$/);
});
