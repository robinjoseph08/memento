import { expect, test } from "@playwright/test";

test("@desktop Public-computer Session disables push and keeps privacy actions prominent", async ({
  page,
}) => {
  let signedIn = false;
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
                csrf_token: "c".repeat(64),
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
      await route.fulfill({ json: { status: "signed_in" } });
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
    if (path === "/api/me/people") {
      await route.fulfill({ json: { people: [] } });
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
  await page.getByText("Sessions and login email").click();
  await expect(
    page.getByRole("button", { name: "Sign out this browser" }),
  ).toBeVisible();
  await expect(page.getByText("Push unavailable")).toBeVisible();
});
