import type { AlbumDetail } from "../app/types/generated/publishing";
import { expect, test } from "./fixtures";

for (const [scenario, dimensions] of [
  [
    "portrait cover",
    [
      [300, 200],
      [100, 400],
      [800, 100],
    ],
  ],
  [
    "panorama cover and portrait video",
    [
      [100, 400],
      [800, 100],
      [100, 400],
    ],
  ],
] as const) {
  test(`Curator overlays fit ${scenario} without letterboxing or oversized tiles`, async ({
    page,
    immich,
  }) => {
    await immich.online();
    await page.goto("/setup");
    await page.getByRole("button", { name: "Claim installation" }).click();
    await expect(page).toHaveURL(/\/curator$/);
    await page.goto("/curator/import?q=Coast");
    await page.getByRole("button", { name: "Import", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Moments", exact: true }),
    ).toBeVisible();
    const path = new URL(page.url()).pathname;
    const album = (await (
      await page.request.get(`/api${path}`)
    ).json()) as AlbumDetail;
    const moment = album.moments[1];
    const entries = moment.entries;
    for (const [index, entry] of entries.entries()) {
      const [width, height] = dimensions[index];
      await page.route(`**${entry.thumbnail_url}`, (route) =>
        route.fulfill({
          contentType: "image/svg+xml",
          body: `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#06b6d4"/></svg>`,
        }),
      );
    }
    const momentRegion = page.getByRole("region", {
      name: new RegExp(`${moment.label}$`),
    });
    await momentRegion.getByRole("button", { expanded: false }).click();
    const media = momentRegion.getByRole("list", { name: "Moment media" });
    await expect(media.getByRole("listitem")).toHaveCount(3);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const entry of entries) {
        const image = media.getByRole("img", {
          name: entry.filename,
          exact: true,
        });
        await expect
          .poll(() =>
            image.evaluate(
              (image: HTMLImageElement) =>
                image.complete && image.naturalWidth > 0,
            ),
          )
          .toBe(true);
        const size = await image.evaluate((image: HTMLImageElement) => ({
          width: image.getBoundingClientRect().width,
          height: image.getBoundingClientRect().height,
          ratio: image.naturalWidth / image.naturalHeight,
          tileWidth: image.closest("li")!.getBoundingClientRect().width,
        }));
        expect(size.width / size.height).toBeCloseTo(size.ratio, 1);
        expect(size.tileWidth - size.width).toBeLessThanOrEqual(8);
        expect(size.height).toBeLessThanOrEqual(257);
        const caption = await image
          .locator("xpath=ancestor::figure")
          .locator("figcaption")
          .evaluate((caption) => ({
            visible: caption.clientWidth,
            text: caption.scrollWidth,
            timeHeight: caption.querySelector("time")!.getBoundingClientRect()
              .height,
            lineHeight: parseFloat(getComputedStyle(caption).lineHeight),
          }));
        expect(caption.text).toBeLessThanOrEqual(caption.visible);
        expect(caption.timeHeight).toBeLessThanOrEqual(caption.lineHeight + 1);
        const overlays = await image.evaluate((image) => {
          const bounds = image.getBoundingClientRect();
          const figure = image.closest("figure")!;
          const time = figure
            .querySelector("figcaption")!
            .getBoundingClientRect();
          const video = figure
            .querySelector('[aria-label="Video"]')
            ?.getBoundingClientRect();
          const cover = figure
            .querySelector('[title="Moment cover"]')
            ?.getBoundingClientRect();
          return {
            image: {
              left: bounds.left,
              right: bounds.right,
              top: bounds.top,
              bottom: bounds.bottom,
            },
            time: {
              left: time.left,
              right: time.right,
              top: time.top,
              bottom: time.bottom,
            },
            video: video
              ? {
                  left: video.left,
                  right: video.right,
                  top: video.top,
                  bottom: video.bottom,
                }
              : null,
            cover: cover
              ? {
                  left: cover.left,
                  right: cover.right,
                  top: cover.top,
                  bottom: cover.bottom,
                }
              : null,
          };
        });
        expect(overlays.time.left).toBeGreaterThanOrEqual(overlays.image.left);
        expect(overlays.time.right).toBeLessThanOrEqual(overlays.image.right);
        expect(overlays.time.top).toBeGreaterThanOrEqual(overlays.image.top);
        expect(overlays.time.bottom).toBeLessThanOrEqual(overlays.image.bottom);
        for (const badge of [overlays.video, overlays.cover]) {
          if (badge)
            expect(
              overlays.time.right <= badge.left ||
                overlays.time.left >= badge.right ||
                overlays.time.bottom <= badge.top ||
                overlays.time.top >= badge.bottom,
            ).toBe(true);
        }
      }
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
    }
  });
}
