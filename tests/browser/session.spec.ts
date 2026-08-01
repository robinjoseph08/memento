import { expect, test } from "@playwright/test";

test("@desktop @mobile Public-computer Session disables push and keeps privacy actions prominent", async ({
  page,
}) => {
  let signedIn = false;
  let sessionGeneration = 0;
  const peopleSearches: unknown[] = [];
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
      return;
    }
    if (path === "/api/session") {
      await route.fulfill(
        signedIn
          ? {
              json: {
                display_name: "Alex",
                session_type: "public",
                csrf_token: (sessionGeneration === 1 ? "c" : "d").repeat(64),
                curator: false,
                onboarding_required: false,
              },
            }
          : { status: 401, json: { error: { message: "sign in" } } },
      );
      return;
    }
    if (path === "/api/auth/sign-in/request") {
      await route.fulfill({
        status: 202,
        json: { challenge_id: "a".repeat(64), status: "accepted" },
      });
      return;
    }
    if (path === "/api/auth/sign-in/verify") {
      signedIn = true;
      sessionGeneration++;
      await route.fulfill({ json: { status: "signed_in" } });
      return;
    }
    if (path === "/api/session/logout") {
      signedIn = false;
      await route.fulfill({ status: 204, body: "" });
      return;
    }
    if (path === "/api/sessions") {
      await route.fulfill({
        json: {
          sessions: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              label: "Library computer",
              browser: "Firefox",
              platform: "Linux",
              session_type: "public",
              created_at: "2026-07-27T12:00:00Z",
              last_activity_at: "2026-07-27T12:00:00Z",
              expires_at: "2026-07-28T00:00:00Z",
              status: "active",
              current: true,
              push_allowed: false,
            },
          ],
        },
      });
      return;
    }
    if (path === "/api/me/interest-list") {
      await route.fulfill({
        json: {
          recipient: { id: "1", display_name: "Alex", sort_name: "alex" },
          version: 0,
          entries: [],
          history: [],
        },
      });
      return;
    }
    if (path === "/api/me/people/search") {
      peopleSearches.push(request.postDataJSON());
      await route.fulfill({ json: { people: [], next_cursor: null } });
      return;
    }
    await route.fulfill({ status: 500, json: { error: { message: path } } });
  });

  await page.goto("/");
  await page.getByLabel("Login email").fill("alex@example.com");
  await page.getByRole("button", { name: "Send sign-in code" }).click();
  await page.getByLabel("Sign-in code").fill("12345678");
  await page
    .getByRole("radio", { name: /Public computer, browser-session/ })
    .check();
  await page.getByRole("button", { name: "Verify and sign in" }).click();

  const warning = page.locator(".public-session-warning");
  await expect(warning).toContainText("Public computer");
  await expect(warning).toContainText("Push is disabled");
  await expect(warning).toContainText(
    "downloaded originals or archives remain",
  );
  await page.getByLabel("Account for Alex").click();
  await page.getByText("Sessions and login email").click();
  await expect(
    page.getByRole("button", { name: "Sign out Library computer" }),
  ).toBeVisible();
  await expect(page.getByText("Push unavailable")).toBeVisible();
  await expect(page.getByText(/created .* last active/)).toBeVisible();

  const privateSearch = page.getByRole("searchbox", {
    name: "Search People available for your Interest list",
  });
  await privateSearch.fill("private query");
  await privateSearch.press("Enter");
  await expect.poll(() => peopleSearches.length).toBeGreaterThanOrEqual(2);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in to Memento" }),
  ).toBeVisible();
  const searchesAfterSignOut = peopleSearches.length;

  await page.getByLabel("Login email").fill("alex@example.com");
  await page.getByRole("button", { name: "Send sign-in code" }).click();
  await page.getByLabel("Sign-in code").fill("12345678");
  await page
    .getByRole("radio", { name: /Public computer, browser-session/ })
    .check();
  await page.getByRole("button", { name: "Verify and sign in" }).click();

  await expect(page.getByLabel("Account for Alex")).toBeVisible();
  await expect(privateSearch).toHaveCount(0);
  await expect(page.locator('input[value="private query"]')).toHaveCount(0);
  expect(peopleSearches).toHaveLength(searchesAfterSignOut);
  await page.getByLabel("Account for Alex").click();
  const reopenedSearch = page.getByRole("searchbox", {
    name: "Search People available for your Interest list",
  });
  await expect(reopenedSearch).toHaveValue("");
  await expect
    .poll(() => peopleSearches.length)
    .toBeGreaterThan(searchesAfterSignOut);
});

test("@desktop @mobile Curator recovery and Recipient lifecycle keep generation actions current", async ({
  page,
}) => {
  const personID = "22222222-2222-4222-8222-222222222222";
  const firstAccessID = "33333333-3333-4333-8333-333333333333";
  const secondAccessID = "44444444-4444-4444-8444-444444444444";
  let access: { id: string; generation: number; state: string } | undefined = {
    id: firstAccessID,
    generation: 1,
    state: "completed",
  };
  let email = "alex@example.com";
  let sessionStatus = "active";
  const mutations: Array<{ path: string; body: unknown }> = [];
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const body: unknown = request.postData()
      ? (request.postDataJSON() as unknown)
      : undefined;
    if (request.method() !== "GET") mutations.push({ path, body });
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
      return;
    }
    if (path === "/api/session") {
      await route.fulfill({
        json: {
          display_name: "Robin",
          session_type: "public",
          csrf_token: "c".repeat(64),
          curator: true,
        },
      });
      return;
    }
    if (path === "/api/sessions") {
      await route.fulfill({ json: { sessions: [] } });
      return;
    }
    if (path === "/api/people") {
      await route.fulfill({
        json: {
          people: [
            {
              id: personID,
              display_name: "Alex",
              sort_name: "alex",
              version: 1,
              status: "current",
              created_at: "2026-07-27T12:00:00Z",
              updated_at: "2026-07-27T12:00:00Z",
              roles: ["recipient"],
              current_recipient_access: access,
              current_login_email: email,
              unrevoked_sessions: sessionStatus === "active" ? 1 : 0,
              historical_audit_count: 0,
            },
          ],
        },
      });
      return;
    }
    if (path === `/api/recipients/${personID}` && request.method() === "GET") {
      if (!access) {
        await route.fulfill({
          status: 404,
          json: { error: { message: "Recipient not found" } },
        });
      } else {
        await route.fulfill({
          json: {
            person_id: personID,
            person_name: "Alex",
            email,
            access,
          },
        });
      }
      return;
    }
    if (path === `/api/recipients/${personID}/sessions`) {
      await route.fulfill({
        json: {
          sessions: [
            {
              id: "55555555-5555-4555-8555-555555555555",
              label: "Alex phone",
              browser: "Safari",
              platform: "iOS",
              session_type: "trusted",
              created_at: "2026-07-27T12:00:00Z",
              last_activity_at: "2026-07-27T12:00:00Z",
              expires_at: "2027-07-27T12:00:00Z",
              status: sessionStatus,
              current: false,
              push_allowed: sessionStatus === "active",
            },
          ],
        },
      });
      return;
    }
    if (path.endsWith("/email-recovery/request")) {
      await route.fulfill({
        status: 202,
        json: {
          recovery_id: "66666666-6666-4666-8666-666666666666",
          expires_at: "2026-07-27T12:10:00Z",
        },
      });
      return;
    }
    if (path.endsWith("/email-recovery/complete")) {
      email = "recovered@example.com";
      sessionStatus = "revoked";
      await route.fulfill({ status: 204 });
      return;
    }
    if (
      ["suspend", "restore", "revoke"].some((action) =>
        path.endsWith(`/${action}`),
      )
    ) {
      const action = path.split("/").at(-1);
      if (action === "suspend") access = { ...access!, state: "suspended" };
      if (action === "restore") access = { ...access!, state: "completed" };
      if (action === "revoke") access = undefined;
      await route.fulfill({
        json: access
          ? { person_id: personID, person_name: "Alex", email, access }
          : {
              person_id: personID,
              person_name: "Alex",
              access: { id: firstAccessID, generation: 1, state: "revoked" },
            },
      });
      return;
    }
    if (path.endsWith("/designate")) {
      access = { id: secondAccessID, generation: 2, state: "pending" };
      email = (body as { email: string }).email;
      await route.fulfill({
        json: { person_id: personID, person_name: "Alex", email, access },
      });
      return;
    }
    if (path.startsWith("/api/relationships")) {
      await route.fulfill({ json: { relationships: [] } });
      return;
    }
    if (path.startsWith("/api/visibility-circles")) {
      await route.fulfill({ json: { circles: [] } });
      return;
    }
    if (path.startsWith("/api/sources")) {
      await route.fulfill({ json: { albums: [], next_cursor: null } });
      return;
    }
    if (path === "/api/repairs") {
      await route.fulfill({
        json: {
          person_candidates: [],
          media_candidates: [],
          unlinked_immich_people: [],
        },
      });
      return;
    }
    if (path === "/api/events") {
      await route.fulfill({ json: { events: [] } });
      return;
    }
    await route.fulfill({
      status: 500,
      json: { error: { message: `Unexpected request: ${path}` } },
    });
  });

  await page.goto("/");
  await page.getByRole("button", { name: /Alex/ }).click();
  await page.getByText("Inspect Recipient Sessions").click();
  await expect(
    page.getByText(/Alex phone.*active.*created.*last active/),
  ).toBeVisible();
  await page
    .getByLabel("Replacement login email")
    .fill("recovered@example.com");
  await page.getByRole("button", { name: "Send recovery code" }).click();
  await page.getByLabel("Recovery code").fill("12345678");
  await page.getByRole("button", { name: "Complete email recovery" }).click();
  await expect(page.getByText(/Alex phone.*revoked/)).toBeVisible();

  await page.getByRole("button", { name: "Suspend Recipient access" }).click();
  await expect(page.getByText(/Generation 1, suspended/)).toBeVisible();
  await page.getByRole("button", { name: "Lift Suspension" }).click();
  await expect(page.getByText(/Generation 1, completed/)).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page
    .getByRole("button", { name: "Revoke Recipient access generation" })
    .click();
  const designate = page.getByRole("button", {
    name: "Designate Pending Recipient",
  });
  await expect(designate).toBeVisible();
  await page
    .locator(".recipient-controls")
    .getByLabel("Login email", { exact: true })
    .fill("alex-again@example.com");
  await designate.click();
  await expect(page.getByText(/Generation 2, pending/)).toBeVisible();

  expect(mutations.find(({ path }) => path.endsWith("/suspend"))?.body).toEqual(
    { access_id: firstAccessID },
  );
  expect(mutations.find(({ path }) => path.endsWith("/revoke"))?.body).toEqual({
    access_id: firstAccessID,
  });
  expect(
    mutations.find(({ path }) => path.endsWith("/designate"))?.body,
  ).toEqual({
    email: "alex-again@example.com",
  });
});
