import { expect, test, type Page } from "@playwright/test";

const trustedSession = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

async function routeRecipientAPI(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
    } else if (path === "/api/session") {
      await route.fulfill({ json: trustedSession });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204 });
    } else if (path === "/api/push" && request.method() === "GET") {
      await route.fulfill({
        json: { available: true, public_key: "AQID", enrolled: false },
      });
    } else if (path === "/api/push/reconcile") {
      await route.fulfill({ json: { enrolled: false, remove_local: false } });
    } else if (path === "/api/push" && request.method() === "PUT") {
      await route.fulfill({
        json: { available: true, public_key: "AQID", enrolled: true },
      });
    } else if (path === "/api/me/photos") {
      await route.fulfill({ json: { media: [], next_cursor: null } });
    } else if (path === "/api/me/new-for-you") {
      await route.fulfill({ json: { events: [] } });
    } else if (path === "/api/me/events") {
      await route.fulfill({ json: { events: [], next_cursor: null } });
    } else if (path === "/api/sessions") {
      await route.fulfill({ json: { sessions: [] } });
    } else if (path === "/api/me/invitation-suggestions") {
      await route.fulfill({ json: { suggestions: [] } });
    } else if (path === "/api/me/interest-list") {
      await route.fulfill({
        json: {
          recipient: { id: "alex", display_name: "Alex", sort_name: "alex" },
          version: 0,
          entries: [],
          history: [],
        },
      });
    } else if (path === "/api/me/people") {
      await route.fulfill({ json: { people: [] } });
    } else {
      await route.fulfill({ status: 404, json: { error: { message: path } } });
    }
  });
}

async function mockPushBrowser(
  page: Page,
  userAgent: string,
  standalone: boolean,
) {
  await page.addInitScript(
    ({ mockedUserAgent, installed }) => {
      const calls = { permission: 0, subscribe: 0, getSubscription: 0 };
      Object.defineProperty(window, "__pushCalls", { value: calls });
      Object.defineProperty(navigator, "userAgent", {
        configurable: true,
        value: mockedUserAgent,
      });
      Object.defineProperty(navigator, "standalone", {
        configurable: true,
        value: installed,
      });
      const subscription = {
        endpoint: "https://push.example/device",
        toJSON: () => ({
          endpoint: "https://push.example/device",
          expirationTime: null,
          keys: { p256dh: "browser-public-key", auth: "browser-auth" },
        }),
        unsubscribe: () => Promise.resolve(true),
      };
      const pushManager = {
        getSubscription: () => {
          calls.getSubscription += 1;
          return Promise.resolve(null);
        },
        subscribe: () => {
          calls.subscribe += 1;
          return Promise.resolve(subscription);
        },
      };
      const registration = {
        active: null,
        waiting: null,
        installing: null,
        pushManager,
        addEventListener: () => undefined,
      };
      Object.defineProperty(navigator, "serviceWorker", {
        configurable: true,
        value: {
          ready: Promise.resolve(registration),
          controller: null,
          register: () => Promise.resolve(registration),
          addEventListener: () => undefined,
          removeEventListener: () => undefined,
        },
      });
      Object.defineProperty(window, "PushManager", {
        configurable: true,
        value: class PushManager {},
      });
      Object.defineProperty(window, "Notification", {
        configurable: true,
        value: {
          permission: "default",
          requestPermission: () => {
            calls.permission += 1;
            return Promise.resolve("granted");
          },
        },
      });
      const originalMatchMedia = window.matchMedia.bind(window);
      window.matchMedia = (query: string) =>
        query === "(display-mode: standalone)"
          ? ({ matches: installed } as MediaQueryList)
          : originalMatchMedia(query);
    },
    { mockedUserAgent: userAgent, installed: standalone },
  );
}

test("@desktop Android capability detection prompts only after the explicit action", async ({
  page,
}) => {
  await mockPushBrowser(
    page,
    "Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 Chrome/136 Mobile",
    false,
  );
  await routeRecipientAPI(page);
  await page.goto("/");

  const enable = page.getByRole("button", {
    name: "Enable push notifications",
  });
  await expect(enable).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as typeof window & { __pushCalls: { permission: number } })
            .__pushCalls.permission,
      ),
    )
    .toBe(0);

  await enable.click();
  await expect(
    page.getByText("Push notifications are enabled on this device."),
  ).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as typeof window & {
              __pushCalls: { permission: number; subscribe: number };
            }
          ).__pushCalls,
      ),
    )
    .toMatchObject({ permission: 1, subscribe: 1 });
});

test("@desktop iPhone and iPad guidance requires Home Screen installation before enabling", async ({
  page,
}) => {
  await mockPushBrowser(
    page,
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_4 like Mac OS X) AppleWebKit/605.1.15 Mobile",
    false,
  );
  await routeRecipientAPI(page);
  await page.goto("/");

  await expect(
    page.getByText(/Add Memento to your iPhone or iPad Home Screen/),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Enable push notifications" }),
  ).toHaveCount(0);
  const calls = await page.evaluate(
    () =>
      (
        window as typeof window & {
          __pushCalls: { permission: number; subscribe: number };
        }
      ).__pushCalls,
  );
  expect(calls).toMatchObject({ permission: 0, subscribe: 0 });
});
