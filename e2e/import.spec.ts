import type { AlbumDetail } from "../app/types/generated/publishing";
import { expect, test } from "./fixtures";

test("imports an album through a stopped task, browser closure, and API restart", async ({
  page,
  context,
  immich,
}) => {
  test.setTimeout(60_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByLabel("Email", { exact: true }).fill("curator@example.com");
  await page.getByLabel("Display name").fill("Fixture Curator");
  await page.getByLabel("Display name").press("Enter");
  await expect(page).toHaveURL(/\/curator$/);
  await page.getByRole("link", { name: "Import an album" }).click();

  await expect(page.getByText("Page 1 of 2", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Next page" }).click();
  await expect(page.getByText("Page 2 of 2", { exact: true })).toBeVisible();
  await page
    .getByRole("searchbox", { name: "Search Immich albums" })
    .fill("Fixture Album");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("article")).toHaveCount(2);
  const source = page.getByRole("article").filter({
    has: page.getByRole("heading", {
      name: "Fixture Album - Coast",
      exact: true,
    }),
  });
  const cover = source.getByRole("img", { name: "Fixture Album - Coast" });
  await expect(cover).toBeVisible();
  await expect
    .poll(() =>
      cover.evaluate(
        (image: HTMLImageElement) => image.complete && image.naturalWidth > 0,
      ),
    )
    .toBe(true);

  await immich.checkpoint("asset-metadata", "fail");
  await source.getByRole("button", { name: "Import", exact: true }).click();
  await expect(page).toHaveURL(/\/curator\/albums\/[^/]+$/);
  const albumURL = page.url();
  const albumID = new URL(albumURL).pathname.split("/").at(-1);
  // River retries upstream failures before exposing a terminal stopped task.
  await expect(
    page.getByRole("heading", { name: "Import failed", exact: true }),
  ).toBeVisible({ timeout: 30_000 });
  expect((await immich.checkpoints())["asset-metadata"].hits).toBeGreaterThan(
    0,
  );

  // Neither closing the page nor replacing the API removes the durable task.
  await page.close();
  await immich.restart();
  const reopened = await context.newPage();
  await reopened.goto(albumURL);
  await expect(
    reopened.getByRole("heading", { name: "Import failed", exact: true }),
  ).toBeVisible();
  await expect(
    reopened.getByRole("button", { name: "Retry import", exact: true }),
  ).toBeVisible();

  await immich.checkpoint("asset-metadata", "open");
  await immich.checkpoint("import-release", "pause");
  await reopened
    .getByRole("button", { name: "Retry import", exact: true })
    .click();
  await expect
    .poll(async () => (await immich.checkpoints())["import-release"].waiting)
    .toBe(1);
  await expect(
    reopened.getByRole("progressbar", { name: "Import progress" }),
  ).toHaveAttribute("value", "5");
  await expect(
    reopened.getByText("5 of 6 items processed", { exact: true }),
  ).toBeVisible();
  await reopened.close();
  await immich.checkpoint("import-release", "open");

  // Wait for the real worker, not a mocked browser response or a fixed delay.
  await expect
    .poll(async () => {
      const response = await context.request.get(
        `/api/curator/albums/${albumID}`,
      );
      expect(response.status()).toBe(200);
      return ((await response.json()) as AlbumDetail).status;
    })
    .toBe("complete");
  const completed = await context.newPage();
  await completed.goto(albumURL);
  await expect(
    completed.getByText("Unpublished", { exact: true }),
  ).toBeVisible();
  await expect(
    completed.getByRole("heading", { name: "Moments", exact: true }),
  ).toBeVisible();
  await expect(
    completed.getByText("4 photos, 2 videos", { exact: true }),
  ).toBeVisible();
  for (const [label, count] of [
    ["Monday, June 1, 2026", 1],
    ["Tuesday, June 2, 2026", 3],
    ["Wednesday, June 3, 2026", 2],
  ] as const) {
    const toggle = completed.getByRole("button", { name: label, exact: true });
    if ((await toggle.getAttribute("aria-expanded")) !== "true")
      await toggle.click();
    const previews = completed
      .getByRole("region", { name: label, exact: true })
      .getByRole("list", { name: "Moment media" })
      .getByRole("img", { name: /\.(jpg|mp4)$/ });
    await expect(previews).toHaveCount(count);
    for (const preview of await previews.all()) {
      await preview.scrollIntoViewIfNeeded();
      await expect
        .poll(() =>
          preview.evaluate(
            (image: HTMLImageElement) =>
              image.complete &&
              image.naturalWidth === 320 &&
              image.naturalHeight === 240,
          ),
        )
        .toBe(true);
      await expect(preview).toHaveAttribute("src", /^\/api\/media\//);
    }
  }
  await completed
    .getByRole("button", { name: "Tuesday, June 2, 2026", exact: true })
    .click();
  const tiedPhotos = completed
    .getByRole("region", { name: "Tuesday, June 2, 2026" })
    .getByRole("list", { name: "Moment media" })
    .getByRole("img", { name: /\.jpg$/ });
  await expect(tiedPhotos).toHaveCount(2);
  expect(
    await tiedPhotos.evaluateAll((images) =>
      images.map((image) => image.getAttribute("alt")),
    ),
  ).toEqual(["coast-02.jpg", "coast-03.jpg"]);

  await completed
    .getByRole("link", { name: "Album details", exact: true })
    .click();
  await completed
    .getByLabel("Album title", { exact: true })
    .fill("Our coast holiday");
  await completed
    .getByRole("button", { name: "Save title", exact: true })
    .click();
  await expect(
    completed.getByRole("heading", { name: "Our coast holiday", exact: true }),
  ).toBeVisible();
  await completed.getByRole("link", { name: "All albums" }).click();
  await expect(
    completed.getByRole("link", {
      name: /Our coast holiday.*4 photos, 2 videos.*unpublished/,
    }),
  ).toBeVisible();
  const albumSearch = completed.getByRole("searchbox", {
    name: "Search albums",
  });
  await albumSearch.fill("not our album");
  await albumSearch.press("Enter");
  await expect(
    completed.getByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  await completed.getByRole("button", { name: "Clear search" }).click();
  await expect(albumSearch).toBeFocused();
  await expect(
    completed.getByRole("link", { name: /Our coast holiday/ }),
  ).toBeVisible();
  await completed.getByRole("link", { name: "Import an album" }).click();
  await completed
    .getByRole("searchbox", { name: "Search Immich albums" })
    .fill("Fixture Album - Coast");
  await completed.getByRole("button", { name: "Search", exact: true }).click();
  await expect(completed.getByRole("article")).toHaveCount(1);
  const membershipReads = (await immich.requests())[
    "POST /api/search/metadata"
  ];
  await completed
    .getByRole("link", { name: "Open album", exact: true })
    .click();
  await expect(completed).toHaveURL(albumURL);
  await expect(
    completed.getByRole("heading", { name: "Our coast holiday", exact: true }),
  ).toBeVisible();
  expect((await immich.requests())["POST /api/search/metadata"]).toBe(
    membershipReads,
  );
  await completed.close();
});
