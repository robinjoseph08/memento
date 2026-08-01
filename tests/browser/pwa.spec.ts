import { createHash } from "node:crypto";

import {
  expect,
  test,
  type BrowserContext,
  type Page,
  type Request,
} from "@playwright/test";

import { startProductionRevisions } from "./support/pwa-production-revisions";

test.use({ serviceWorkers: "allow" });

const session = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

const publicCachePaths = new Set([
  "/",
  "/manifest.webmanifest",
  "/icon.svg",
  "/favicon.ico",
  "/apple-touch-icon.png",
  "/icon-192.png",
  "/icon-512.png",
  "/icon-mask.png",
  "/icon-monochrome.png",
]);

const privatePhoto = {
  id: "private-photo",
  media_type: "image",
  width: 1600,
  height: 900,
  local_date_time: "2026-07-27T12:00:00Z",
  capture_date: "2026-07-27",
  available: true,
  thumbnail_url: "/api/me/media/private-photo/thumbnail",
  preview_url: "/api/me/media/private-photo/preview",
  video_url: "",
  original_url: "/api/me/media/private-photo/original",
};

async function recipientAPI(page: Page, media: object[] = []) {
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
    } else if (path === "/api/me/photos/chronology") {
      await route.fulfill({
        json: {
          dates: media.length
            ? [
                {
                  capture_date: "2026-07-27",
                  media_count: media.length,
                  cursor: "",
                },
              ]
            : [],
        },
      });
    } else if (path === "/api/me/photos") {
      await route.fulfill({ json: { media, next_cursor: null } });
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
    } else if (path === "/api/me/people/search") {
      await route.fulfill({ json: { people: [], next_cursor: null } });
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

function pngSize(bytes: Buffer) {
  expect(bytes.subarray(1, 4).toString("ascii")).toBe("PNG");
  return {
    width: bytes.readUInt32BE(16),
    height: bytes.readUInt32BE(20),
  };
}

function observeRuntimeRequests(context: BrowserContext) {
  const requests: Request[] = [];
  const pending = new Set<Request>();
  let activityVersion = 0;

  context.on("request", (request) => {
    requests.push(request);
    pending.add(request);
    activityVersion += 1;
  });
  const finish = (request: Request) => {
    pending.delete(request);
    activityVersion += 1;
  };
  context.on("requestfinished", finish);
  context.on("requestfailed", finish);

  return {
    requests,
    async waitForQuiescence(page: Page) {
      let previousActivityVersion = -1;
      let stableObservations = 0;
      await expect
        .poll(
          async () => {
            const lifecycle = await page.evaluate(async () => {
              const registration =
                await navigator.serviceWorker.getRegistration();
              return {
                document: document.readyState,
                fonts: document.fonts.status,
                serviceWorker: registration?.active?.state ?? "missing",
              };
            });
            if (
              pending.size === 0 &&
              activityVersion === previousActivityVersion
            ) {
              stableObservations += 1;
            } else {
              stableObservations = 0;
            }
            previousActivityVersion = activityVersion;
            return {
              settled:
                lifecycle.document === "complete" &&
                lifecycle.fonts === "loaded" &&
                lifecycle.serviceWorker === "activated" &&
                stableObservations >= 3,
              lifecycle,
              activityVersion,
              requestCount: requests.length,
              pending: [...pending].map((request) => request.url()),
              recent: requests.slice(-8).map((request) => ({
                url: request.url(),
                serviceWorker: Boolean(request.serviceWorker()),
              })),
            };
          },
          {
            intervals: [100, 250, 500],
            timeout: 5_000,
            message:
              "runtime requests did not reach browser-lifecycle quiescence",
          },
        )
        .toMatchObject({ settled: true });
    },
  };
}

async function cachedURLs(page: Page) {
  return page.evaluate(async () => {
    const names = await caches.keys();
    const requests = await Promise.all(
      names.map(async (name) => (await caches.open(name)).keys()),
    );
    return requests.flat().map((cachedRequest) => cachedRequest.url);
  });
}

test("@desktop manifest, browser installability, scoped restart, and cache privacy", async ({
  browserName,
  context,
  page,
  request,
}) => {
  const runtimeObservation = observeRuntimeRequests(context);
  await recipientAPI(page);

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
        src: "/icon-192.png",
        sizes: "192x192",
        type: "image/png",
        purpose: "any",
      }),
      expect.objectContaining({
        src: "/icon-512.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "any",
      }),
      expect.objectContaining({
        src: "/icon-mask.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "maskable",
      }),
      expect.objectContaining({
        src: "/icon-monochrome.png",
        sizes: "512x512",
        type: "image/png",
        purpose: "monochrome",
      }),
    ]),
  );

  const expectedIcons = [
    {
      path: "/icon-192.png",
      size: 192,
      digest:
        "14dc849101076129c52788a5cedff30f16b9d461b7a8b4d92685c007fb95d037",
    },
    {
      path: "/icon-512.png",
      size: 512,
      digest:
        "f40858ec22eef6f75403c9ec8967c7688658af547da79d8b1b93168606f878f9",
    },
    {
      path: "/icon-mask.png",
      size: 512,
      digest:
        "34bf30cd5e3446248d0f0fee6cb3ac7f7d03788fcc0236956251acb94f53a025",
    },
    {
      path: "/icon-monochrome.png",
      size: 512,
      digest:
        "b9c97949eb395c63748663400ca047dc503e7a5d276e1326e83ca1f9c1660e53",
    },
    {
      path: "/apple-touch-icon.png",
      size: 180,
      digest:
        "fd101998f3dd99daf50a97e6b52c8d25935feea51a2244ec868afe871ce8ed52",
    },
  ];
  for (const icon of expectedIcons) {
    const response = await request.get(icon.path);
    expect(response.headers()["content-type"]).toBe("image/png");
    const bytes = await response.body();
    expect(pngSize(bytes)).toEqual({ width: icon.size, height: icon.size });
    expect(createHash("sha256").update(bytes).digest("hex")).toBe(icon.digest);
  }

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

    const protectedPaths = [
      "/api/me/media/11111111-1111-4111-8111-111111111111/thumbnail",
      "/protected-media/family-photo.jpg",
      "/private-gallery/alex/library.json",
      "/archives/family-weekend.zip",
    ];
    for (const path of protectedPaths.slice(1)) {
      await page.route(`**${path}`, (route) =>
        route.fulfill({
          body: "private response",
          headers: { "Cache-Control": "private, no-store" },
        }),
      );
    }
    await page.evaluate(async (paths) => {
      const responses = await Promise.all(paths.map((path) => fetch(path)));
      await Promise.all(responses.map((response) => response.arrayBuffer()));
    }, protectedPaths);

    const rejectedCachePaths = {
      mismatched: "/assets/missing-production.js",
      query: "/icon.svg?recipient=alex",
      undiscovered: "/assets/private-export.js",
    };
    await page.route(/\/icon\.svg\?recipient=alex$/, (route) =>
      route.fulfill({ body: "<svg></svg>", contentType: "image/svg+xml" }),
    );
    await page.route(`**${rejectedCachePaths.undiscovered}`, (route) =>
      route.fulfill({
        body: "export default 'private';",
        contentType: "text/javascript",
      }),
    );
    const admission = await page.evaluate(async (paths) => {
      const cacheName = (await caches.keys()).find((name) =>
        name.startsWith("memento-shell-"),
      );
      if (!cacheName) throw new Error("Memento shell cache is missing");
      const cache = await caches.open(cacheName);
      await cache.put(
        paths.mismatched,
        new Response("known JavaScript", {
          headers: { "Content-Type": "text/javascript" },
        }),
      );
      const [mismatched] = await Promise.all([
        fetch(paths.mismatched, { cache: "reload" }),
        fetch(paths.query, { cache: "reload" }),
        fetch(paths.undiscovered, { cache: "reload" }),
      ]);
      const result = {
        mismatchedCache: await (await cache.match(paths.mismatched))?.text(),
        mismatchedType: mismatched.headers.get("Content-Type"),
      };
      await cache.delete(paths.mismatched);
      return result;
    }, rejectedCachePaths);

    expect(admission.mismatchedType).toMatch(/^text\/html(?:;|$)/);
    expect(admission.mismatchedCache).toBe("known JavaScript");

    const cacheEntries = await cachedURLs(page);
    const cachePaths = cacheEntries.map((url) => new URL(url).pathname);
    expect(cachePaths).toContain("/");
    expect(
      cachePaths.some((path) => /^\/assets\/index-[\w-]+\.js$/.test(path)),
    ).toBe(true);
    expect(
      cachePaths.some((path) => /^\/assets\/index-[\w-]+\.css$/.test(path)),
    ).toBe(true);
    expect(
      cachePaths.some((path) =>
        /^\/assets\/dm-sans-[\w-]+\.woff2?$/.test(path),
      ),
    ).toBe(true);
    expect(
      cacheEntries.every((url) => {
        const cached = new URL(url);
        return (
          cached.origin === new URL(page.url()).origin &&
          cached.search === "" &&
          (publicCachePaths.has(cached.pathname) ||
            cached.pathname.startsWith("/assets/"))
        );
      }),
    ).toBe(true);
    expect(cachePaths).toEqual(expect.not.arrayContaining(protectedPaths));
    expect(cachePaths).not.toContain(rejectedCachePaths.mismatched);
    expect(cachePaths).not.toContain(rejectedCachePaths.undiscovered);
    expect(cacheEntries).not.toContain(
      new URL(rejectedCachePaths.query, page.url()).href,
    );
  }

  await runtimeObservation.waitForQuiescence(page);
  const applicationOrigin = new URL(page.url()).origin;
  const thirdPartyRequests = runtimeObservation.requests
    .filter(
      (networkRequest) =>
        new URL(networkRequest.url()).origin !== applicationOrigin,
    )
    .map((networkRequest) => ({
      url: networkRequest.url(),
      serviceWorker: Boolean(networkRequest.serviceWorker()),
    }));
  expect(
    thirdPartyRequests,
    "runtime requests must stay first-party after lifecycle quiescence",
  ).toEqual([]);
  if (browserName === "chromium") {
    expect(
      runtimeObservation.requests.some((networkRequest) =>
        networkRequest.serviceWorker(),
      ),
    ).toBe(true);
  }
});

test("@desktop production graph revisions update at the stable worker URL once", async ({
  browser,
  browserName,
}) => {
  test.skip(browserName !== "chromium", "Chromium runs the update lifecycle");

  const deployment = await startProductionRevisions();
  const context = await browser.newContext({ serviceWorkers: "allow" });
  const page = await context.newPage();
  try {
    expect(deployment.first.workerDigest).not.toBe(
      deployment.second.workerDigest,
    );
    expect(deployment.first.workerRevision).not.toBe(
      deployment.second.workerRevision,
    );
    expect(deployment.first.graphPaths).not.toEqual(
      deployment.second.graphPaths,
    );

    await page.addInitScript((updateEvent) => {
      window.addEventListener(updateEvent, () => {
        const count = Number(sessionStorage.getItem(updateEvent) ?? "0");
        sessionStorage.setItem(updateEvent, String(count + 1));
      });
    }, "memento-pwa-update");
    await recipientAPI(page);
    await page.goto(`${deployment.origin}/photos`);
    await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(async () => {
          await navigator.serviceWorker.ready;
          return navigator.serviceWorker.controller?.state ?? "";
        }),
      )
      .toBe("activated");

    const workerURL = `${deployment.origin}/service-worker.js`;
    const firstWorkerResponse = await context.request.get(workerURL);
    expect(firstWorkerResponse.ok()).toBe(true);
    expect(
      createHash("sha256")
        .update(await firstWorkerResponse.body())
        .digest("hex"),
    ).toBe(deployment.first.workerDigest);
    const firstCacheName = `memento-shell-${deployment.first.workerRevision}`;
    await expect
      .poll(() => page.evaluate(() => caches.keys()))
      .toEqual([firstCacheName]);

    const firstOnlyGraphPaths = deployment.first.graphPaths.filter(
      (path) => !deployment.second.graphPaths.includes(path),
    );
    const secondOnlyGraphPaths = deployment.second.graphPaths.filter(
      (path) => !deployment.first.graphPaths.includes(path),
    );
    expect(firstOnlyGraphPaths).not.toEqual([]);
    expect(secondOnlyGraphPaths).not.toEqual([]);

    deployment.activateSecond();
    const secondWorkerResponse = await context.request.get(workerURL);
    expect(secondWorkerResponse.ok()).toBe(true);
    expect(
      createHash("sha256")
        .update(await secondWorkerResponse.body())
        .digest("hex"),
    ).toBe(deployment.second.workerDigest);

    const workerUpdateRequests: string[] = [];
    context.on("request", (networkRequest) => {
      if (new URL(networkRequest.url()).pathname === "/service-worker.js") {
        workerUpdateRequests.push(networkRequest.url());
      }
    });
    await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.ready;
      await registration.update();
    });

    await expect(page.getByText("A Memento update is ready.")).toBeVisible();
    expect(
      await page.evaluate(() => sessionStorage.getItem("memento-pwa-update")),
    ).toBe("1");
    expect(workerUpdateRequests.length).toBeGreaterThan(0);
    expect(workerUpdateRequests.every((url) => url === workerURL)).toBe(true);

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
      .toBe(workerURL);
    const secondCacheName = `memento-shell-${deployment.second.workerRevision}`;
    await expect
      .poll(() => page.evaluate(() => caches.keys()))
      .toEqual([secondCacheName]);
    expect(await page.evaluate(() => caches.keys())).not.toContain(
      firstCacheName,
    );
    const revisedCachePaths = (await cachedURLs(page)).map(
      (url) => new URL(url).pathname,
    );
    expect(revisedCachePaths).toEqual(
      expect.arrayContaining(deployment.second.graphPaths),
    );
    expect(revisedCachePaths).toEqual(
      expect.not.arrayContaining(firstOnlyGraphPaths),
    );
    expect(revisedCachePaths).toEqual(
      expect.arrayContaining(secondOnlyGraphPaths),
    );
    await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();

    const stableRevision = await page.evaluate(async () => {
      const registration = await navigator.serviceWorker.ready;
      await registration.update();
      return {
        active: registration.active?.state,
        waiting: registration.waiting?.state ?? null,
      };
    });
    expect(stableRevision).toEqual({ active: "activated", waiting: null });
    expect(
      await page.evaluate(() => sessionStorage.getItem("memento-pwa-update")),
    ).toBe("1");
    await expect(page.getByText("A Memento update is ready.")).toHaveCount(0);
  } finally {
    await context.close();
    await deployment.close();
  }
});

test("@desktop protected HTML navigation cannot replace the public shell cache", async ({
  browserName,
  context,
  page,
}) => {
  test.skip(
    browserName !== "chromium",
    "Chromium exposes service-worker response attribution",
  );
  await recipientAPI(page);
  await page.goto("/");
  await expect
    .poll(() =>
      page.evaluate(async () => {
        await navigator.serviceWorker.ready;
        return navigator.serviceWorker.controller?.state ?? "";
      }),
    )
    .toBe("activated");

  const readCachedShell = () =>
    page.evaluate(async () => {
      const cacheName = (await caches.keys()).find((name) =>
        name.startsWith("memento-shell-"),
      );
      if (!cacheName) throw new Error("Memento shell cache is missing");
      return (await (await caches.open(cacheName)).match("/"))?.text();
    });
  const shellBefore = await readCachedShell();
  expect(shellBefore).toBeTruthy();

  const protectedPath = "/protected-media/private-report";
  const navigationEvidence: Array<{
    navigation: boolean;
    serviceWorker: boolean;
  }> = [];
  await context.route(`**${protectedPath}`, (route) => {
    const request = route.request();
    navigationEvidence.push({
      navigation: request.isNavigationRequest(),
      serviceWorker: Boolean(request.serviceWorker()),
    });
    return route.fulfill({
      body: "<!doctype html><title>Private report</title><p>private navigation marker</p>",
      contentType: "text/html",
      headers: { "Cache-Control": "private" },
    });
  });

  const navigation = await page.goto(protectedPath, {
    waitUntil: "domcontentloaded",
  });

  expect(navigation?.fromServiceWorker()).toBe(true);
  expect(navigation?.request().isNavigationRequest()).toBe(true);
  await expect(page.getByText("private navigation marker")).toBeVisible();
  expect(navigationEvidence).toContainEqual({
    navigation: false,
    serviceWorker: true,
  });
  expect(await readCachedShell()).toBe(shellBefore);
  expect(await readCachedShell()).not.toContain("private navigation marker");
});

test("@mobile compact navigation and theme control remain usable", async ({
  page,
}) => {
  await recipientAPI(page);
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

test("@desktop service worker opens a production deep link offline without private restart data", async ({
  browserName,
  context,
  page,
}) => {
  test.skip(
    browserName !== "chromium",
    "Chromium exposes service-worker response attribution",
  );
  await recipientAPI(page, [privatePhoto]);
  await page.goto("/photos");
  await expect(page.getByAltText("Photo 1 from July 2026")).toBeVisible();

  await expect
    .poll(() =>
      page.evaluate(async () => {
        await navigator.serviceWorker.ready;
        return navigator.serviceWorker.controller?.state ?? "";
      }),
    )
    .toBe("activated");
  const productionCachePaths = (await cachedURLs(page)).map(
    (url) => new URL(url).pathname,
  );
  expect(
    productionCachePaths.some((path) =>
      /^\/assets\/index-[\w-]+\.js$/.test(path),
    ),
  ).toBe(true);
  expect(
    productionCachePaths.some((path) =>
      /^\/assets\/index-[\w-]+\.css$/.test(path),
    ),
  ).toBe(true);

  await page.unroute("**/api/**");
  const workerResponses: string[] = [];
  context.on("response", (response) => {
    if (response.fromServiceWorker()) workerResponses.push(response.url());
  });
  await context.setOffline(true);
  const navigation = await page.goto("/events/family-weekend", {
    waitUntil: "domcontentloaded",
  });

  expect(navigation?.fromServiceWorker()).toBe(true);
  expect(new URL(page.url()).pathname).toBe("/events/family-weekend");
  await expect(
    page.getByRole("heading", { name: "Memento is offline" }),
  ).toBeVisible();
  await expect(
    page.getByText(/Memento's offline cache does not store protected photos/),
  ).toBeVisible();
  await expect(page.getByAltText("Photo 1 from July 2026")).toHaveCount(0);
  await expect(page.getByText("No photos are available.")).toHaveCount(0);
  await expect(page.locator(".justified-gallery")).toHaveCount(0);
  expect(
    workerResponses.some(
      (url) => new URL(url).pathname === "/events/family-weekend",
    ),
  ).toBe(true);
});
