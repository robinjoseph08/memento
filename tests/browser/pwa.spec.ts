import { expect, test, type Page } from "@playwright/test";

test.use({ serviceWorkers: "allow" });

const session = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

async function emptyRecipientAPI(page: Page) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/setup") {
      await route.fulfill({
        status: 404,
        json: { error: { message: "closed" } },
      });
    } else if (path === "/api/session") {
      await route.fulfill({ json: session });
    } else if (path === "/api/session/refresh") {
      await route.fulfill({ status: 204, body: "" });
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
          recipient: {
            id: "recipient",
            display_name: "Alex",
            sort_name: "alex",
          },
          version: 0,
          entries: [],
          history: [],
        },
      });
    } else if (path === "/api/me/people") {
      await route.fulfill({ json: { people: [] } });
    } else if (path.startsWith("/api/me/media/")) {
      await route.fulfill({
        contentType: "image/png",
        body: Buffer.from(
          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
          "base64",
        ),
      });
    } else {
      await route.fulfill({
        status: 404,
        json: { error: { message: "not found" } },
      });
    }
  });
}

test("@desktop manifest, browser installability, scoped restart, and cache privacy", async ({
  browserName,
  page,
  request,
}) => {
  const runtimeRequests: string[] = [];
  page.on("request", (networkRequest) =>
    runtimeRequests.push(networkRequest.url()),
  );
  await emptyRecipientAPI(page);

  const manifestResponse = await request.get("/manifest.webmanifest");
  expect(manifestResponse.ok()).toBe(true);
  const manifest: unknown = await manifestResponse.json();
  expect(manifest).toMatchObject({
    id: "/",
    name: "Memento",
    start_url: "/",
    scope: "/",
    display: "standalone",
    theme_color: "#020617",
  });
  if (!manifest || typeof manifest !== "object" || !("icons" in manifest)) {
    throw new Error("Memento manifest icons are missing");
  }
  expect(manifest.icons).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        src: "/icon-512.png",
        sizes: "512x512",
        purpose: "any",
      }),
      expect.objectContaining({
        src: "/icon-mask.png",
        sizes: "512x512",
        purpose: "maskable",
      }),
    ]),
  );

  await page.goto("/photos");
  await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
  await expect(page.locator(".library-rail")).toBeVisible();
  await expect(page.locator(".mobile-library-nav")).toBeHidden();
  await expect(page.getByText("No photos are available.")).toBeVisible();
  await expect(page.locator("html")).toHaveCSS("color-scheme", "dark");
  expect(await page.locator('link[rel="manifest"]').getAttribute("href")).toBe(
    "/manifest.webmanifest",
  );

  await page.getByRole("button", { name: "Use light theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator("html")).toHaveCSS("color-scheme", "light");
  await expect(page.locator(".recipient-library")).toHaveCSS(
    "background-color",
    "rgb(248, 250, 252)",
  );
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(
    page.getByRole("button", { name: "Use dark theme" }),
  ).toBeVisible();

  if (browserName === "chromium") {
    const cdp = await page.context().newCDPSession(page);
    const { installabilityErrors } = await cdp.send(
      "Page.getInstallabilityErrors",
    );
    expect(installabilityErrors).toEqual([]);

    const workerState = await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.ready;
      await registration.update();
      return {
        active: registration.active?.state,
        script: registration.active?.scriptURL,
      };
    });
    expect(workerState.active).toBe("activated");
    expect(workerState.script).toMatch(/\/service-worker\.js$/);

    await page.evaluate(() =>
      fetch("/api/me/media/11111111-1111-4111-8111-111111111111/thumbnail"),
    );
    const cachedURLs = await page.evaluate(async () => {
      const names = await caches.keys();
      const requests = await Promise.all(
        names.map(async (name) => (await caches.open(name)).keys()),
      );
      return requests.flat().map((cachedRequest) => cachedRequest.url);
    });
    expect(cachedURLs.some((url) => new URL(url).pathname === "/")).toBe(true);
    expect(
      cachedURLs.every((url) => !new URL(url).pathname.startsWith("/api")),
    ).toBe(true);

    await page.evaluate(() =>
      navigator.serviceWorker.register(
        "/service-worker.js?browser-update=second",
        { scope: "/" },
      ),
    );
    await expect(page.getByText("A Memento update is ready.")).toBeVisible();

    const restarted = page.waitForEvent("framenavigated", {
      predicate: (frame) => frame === page.mainFrame(),
    });
    await page.getByRole("button", { name: "Update and restart" }).click();
    await restarted;

    await expect
      .poll(() =>
        page.evaluate(
          () => navigator.serviceWorker.controller?.scriptURL ?? "",
        ),
      )
      .toContain("/service-worker.js?browser-update=second");
    await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
  }

  const applicationOrigin = new URL(page.url()).origin;
  expect(
    runtimeRequests.every((url) => new URL(url).origin === applicationOrigin),
  ).toBe(true);
});

test("@mobile compact navigation and theme control remain usable", async ({
  page,
}) => {
  await emptyRecipientAPI(page);
  await page.goto("/");

  await expect(page.locator(".mobile-library-nav")).toBeVisible();
  await expect(page.locator(".library-rail")).toBeHidden();
  await page
    .locator(".mobile-library-nav")
    .getByRole("button", { name: "Events" })
    .click();
  await expect(page.getByRole("heading", { name: "Events" })).toBeVisible();
  await page
    .locator(".mobile-library-nav")
    .getByRole("button", { name: "Favorites" })
    .click();
  await expect(page.getByRole("heading", { name: "Favorites" })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Use light theme" }),
  ).toBeVisible();
});

test("@desktop restart without API network shows unavailable, never an empty library", async ({
  page,
}) => {
  await emptyRecipientAPI(page);
  await page.goto("/");
  await expect(page.getByText("No photos are available.")).toBeVisible();

  await page.unroute("**/api/**");
  await page.route("**/api/**", (route) => route.abort("internetdisconnected"));
  await page.addInitScript(() => {
    Object.defineProperty(Navigator.prototype, "onLine", {
      configurable: true,
      get: () => false,
    });
  });
  await page.reload();

  await expect(
    page.getByRole("heading", { name: "Memento is offline" }),
  ).toBeVisible();
  await expect(page.getByText(/never saved for offline viewing/)).toBeVisible();
  await expect(page.getByText("No photos are available.")).toHaveCount(0);
  await expect(page.locator(".justified-gallery")).toHaveCount(0);
});
