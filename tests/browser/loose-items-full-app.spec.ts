import { expect, test, type Page, type Request } from "@playwright/test";

const projectIndexes: Record<string, number> = {
  "chromium-desktop": 1,
  "firefox-desktop": 2,
  "chromium-mobile": 3,
  "firefox-mobile": 4,
};

function fixtureID(prefix: string, index: number) {
  return `${prefix}-0000-4000-8000-${String(index).padStart(12, "0")}`;
}

function projectFixture(projectName: string) {
  const index = projectIndexes[projectName];
  if (!index) throw new Error(`No browser fixture for ${projectName}.`);
  return {
    credential: (0x10 + index).toString(16).padStart(2, "0").repeat(32),
    looseID: fixtureID("10000000", index),
    missingLooseID: fixtureID("11000000", index),
    pendingPersonID: fixtureID("40000000", index),
    deniedPersonID: fixtureID("50000000", index),
    title: `Garden portrait project ${index}`,
  };
}

function isMobile(page: Page) {
  return (page.viewportSize()?.width ?? 1280) <= 1024;
}

async function openReviewPane(page: Page) {
  if (isMobile(page)) {
    const button = page.getByRole("button", { name: "Review", exact: true });
    await button.click();
    await expect(button).toBeFocused();
  }
  await expect(
    page.getByRole("heading", { name: "Audience", exact: true }),
  ).toBeVisible();
}

async function openLooseDetailsPane(page: Page) {
  if (isMobile(page)) {
    const button = page.getByRole("button", {
      name: "Loose item",
      exact: true,
    });
    await button.click();
    await expect(button).toBeFocused();
  }
  await expect(
    page.getByRole("heading", { name: "Loose item details" }),
  ).toBeVisible();
}

function requestPath(request: Request) {
  return new URL(request.url()).pathname;
}

function mutationRequest(page: Page, method: string, path: string) {
  return page.waitForRequest(
    (request) => request.method() === method && requestPath(request) === path,
  );
}

function assertCSRF(request: Request, csrfToken: string) {
  expect(request.headers()["x-memento-csrf"] === csrfToken).toBe(true);
}

test("@desktop @mobile persists a real Loose correction, Withdrawal, and restoration", async ({
  baseURL,
  context,
  page,
}, testInfo) => {
  expect(baseURL).toBeTruthy();
  const fixture = projectFixture(testInfo.project.name);
  await context.addCookies([
    {
      name: "__Host-memento_session",
      value: fixture.credential,
      domain: "127.0.0.1",
      path: "/",
      httpOnly: true,
      secure: true,
      sameSite: "Lax",
    },
  ]);

  const forbiddenRequests: string[] = [];
  const forbiddenResponses: string[] = [];
  page.on("request", (request) => {
    const path = requestPath(request);
    if (
      ["/comments", "/favorites", "/archives", "/engagement", "/original"].some(
        (forbidden) => path.includes(forbidden),
      )
    )
      forbiddenRequests.push(`${request.method()} ${path}`);
  });
  page.on("response", (response) => {
    if (response.status() === 403)
      forbiddenResponses.push(
        `${response.request().method()} ${new URL(response.url()).pathname}`,
      );
  });

  const sessionResponse = page.waitForResponse(
    (response) =>
      response.request().method() === "GET" &&
      new URL(response.url()).pathname === "/api/session" &&
      response.ok(),
  );
  await page.goto(`/?workspace=drafts&loose=${fixture.looseID}`);
  const session = (await (await sessionResponse).json()) as {
    csrf_token: string;
  };
  const csrfToken = session.csrf_token;
  expect(csrfToken).toMatch(/^[0-9a-f]{64}$/);

  await expect(
    page.getByRole("heading", { name: fixture.title, exact: true }),
  ).toBeVisible();
  await openReviewPane(page);

  await page
    .getByLabel("Preview Recipient")
    .selectOption(fixture.pendingPersonID);
  await expect(
    page.getByText(/Pending Recipient: cannot access yet/),
  ).toBeVisible();
  const pendingPreviewRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.looseID}/preview`,
  );
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  const pendingPreview = await pendingPreviewRequest;
  expect(pendingPreview.postData()).toBeNull();
  assertCSRF(pendingPreview, csrfToken);
  const preview = page.getByRole("region", {
    name: "Read-only Recipient preview",
  });
  await expect(preview).toContainText("1 authorized Media item");
  for (const action of ["Comment", "Favorite", "Settings", "Download"])
    await expect(preview.getByRole("button", { name: action })).toBeDisabled();

  await openLooseDetailsPane(page);
  const correctedDescription = `Private corrected description ${testInfo.project.name}`;
  const updateRequest = mutationRequest(
    page,
    "PUT",
    `/api/loose-items/${fixture.looseID}`,
  );
  await page.getByLabel("Description").fill(correctedDescription);
  const update = await updateRequest;
  expect(update.postDataJSON()).toEqual({
    version: 4,
    title: fixture.title,
    description: correctedDescription,
    grouping_timezone: "UTC",
    proposed_day: "2026-08-01",
    place_labels: ["Garden"],
  });
  assertCSRF(update, csrfToken);
  await expect(page.getByText("All changes saved")).toBeVisible();
  await expect(
    page.getByText(
      /This correction remains private. Recipients continue to see the current Publication/,
    ),
  ).toBeVisible();

  await openReviewPane(page);
  await expect(preview).toHaveCount(0);
  const correctionPublicationRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.looseID}/publications`,
  );
  await page
    .getByRole("button", { name: "Publish Loose item correction" })
    .click();
  const correctionPublication = await correctionPublicationRequest;
  expect(correctionPublication.postDataJSON()).toEqual({
    version: 5,
    notify_recipients: true,
  });
  assertCSRF(correctionPublication, csrfToken);
  await expect(
    page.getByText("Published Loose item revision 2."),
  ).toBeVisible();

  await page.reload();
  await expect(
    page.getByRole("heading", { name: fixture.title, exact: true }),
  ).toBeVisible();
  await openLooseDetailsPane(page);
  await expect(page.getByLabel("Description")).toHaveValue(
    correctedDescription,
  );
  await openReviewPane(page);

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByLabel("Attributable reason").fill("Privacy correction");
  const withdrawalRequest = mutationRequest(page, "POST", "/api/withdrawals");
  await page
    .getByRole("button", { name: "Withdraw Loose item access" })
    .click();
  const withdrawal = await withdrawalRequest;
  expect(withdrawal.postDataJSON()).toEqual({
    target_kind: "loose_item",
    target_id: fixture.looseID,
    reason: "Privacy correction",
  });
  assertCSRF(withdrawal, csrfToken);
  await expect(
    page.getByText(/Access withdrawn immediately for 1 Recipients/),
  ).toBeVisible();
  await expect(page.getByText(/Access remains withdrawn/)).toBeVisible();

  await openLooseDetailsPane(page);
  await expect(
    page.getByText("Next action: Fresh Audience review"),
  ).toBeVisible();
  await openReviewPane(page);
  await expect(
    page.getByRole("button", { name: "Preview as Recipient" }),
  ).toBeDisabled();

  const approvalRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.looseID}/audience/approve`,
  );
  await page.getByRole("button", { name: "Approve Audience" }).click();
  const approval = await approvalRequest;
  expect(approval.postData()).toBeNull();
  expect(approval.headers()["if-match"]).toBe("5");
  assertCSRF(approval, csrfToken);
  await expect(
    page.getByRole("button", { name: "Publish Loose item restoration" }),
  ).toBeEnabled();

  await page
    .getByLabel("Preview Recipient")
    .selectOption(fixture.deniedPersonID);
  const deniedPreviewRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.looseID}/preview`,
  );
  await page.getByRole("button", { name: "Preview as Recipient" }).click();
  const deniedPreview = await deniedPreviewRequest;
  expect(deniedPreview.postData()).toBeNull();
  assertCSRF(deniedPreview, csrfToken);
  await expect(
    page.getByText("Nothing is shared with this Recipient."),
  ).toBeVisible();
  await expect(preview).not.toContainText("authorized Media item");

  const restorationPublicationRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.looseID}/publications`,
  );
  await page
    .getByRole("button", { name: "Publish Loose item restoration" })
    .click();
  const restorationPublication = await restorationPublicationRequest;
  expect(restorationPublication.postDataJSON()).toEqual({
    version: 7,
    notify_recipients: true,
  });
  assertCSRF(restorationPublication, csrfToken);
  await expect(
    page.getByText("Published Loose item revision 3."),
  ).toBeVisible();
  await expect(page.getByText(/Restored by a later Publication/)).toBeVisible();

  await page.reload();
  await expect(
    page.getByRole("heading", { name: fixture.title, exact: true }),
  ).toBeVisible();
  await openLooseDetailsPane(page);
  await expect(page.getByLabel("Description")).toHaveValue(
    correctedDescription,
  );
  await openReviewPane(page);
  await expect(page.getByText(/Restored by a later Publication/)).toBeVisible();
  await expect(page.getByText(/Access remains withdrawn/)).toHaveCount(0);

  await page.goto(`/?workspace=drafts&loose=${fixture.missingLooseID}`);
  await expect(
    page.getByRole("heading", {
      name: `Missing source project ${projectIndexes[testInfo.project.name]}`,
      exact: true,
    }),
  ).toBeVisible();
  await openReviewPane(page);
  const missingPublicationResponse = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/loose-items/${fixture.missingLooseID}/publications`,
  );
  const missingPublicationRequest = mutationRequest(
    page,
    "POST",
    `/api/loose-items/${fixture.missingLooseID}/publications`,
  );
  await page.getByRole("button", { name: "Publish Loose item" }).click();
  const missingRequest = await missingPublicationRequest;
  assertCSRF(missingRequest, csrfToken);
  expect(missingRequest.postDataJSON()).toEqual({
    version: 2,
    notify_recipients: true,
  });
  expect((await missingPublicationResponse).status()).toBe(409);
  await expect(page.getByText(/Source Media is unavailable/)).toBeVisible();
  await expect(page.getByText(/Published Loose item revision/)).toHaveCount(0);
  await page.reload();
  await openReviewPane(page);
  await expect(
    page.getByRole("button", { name: "Publish Loose item" }),
  ).toBeEnabled();

  expect(forbiddenRequests).toEqual([]);
  expect(forbiddenResponses).toEqual([]);
});
