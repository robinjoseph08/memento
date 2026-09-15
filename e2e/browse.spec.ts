import { mkdir } from "node:fs/promises";
import type { Locator, Page } from "@playwright/test";

import type {
  ViewerAlbum,
  ViewerEntry,
  ViewerPage,
} from "../app/types/generated/publishing";
import { expect, finishOnboarding, test } from "./fixtures";

async function captureLayouts(page: Page, name: string) {
  if (process.env.QA_CAPTURES !== "1") return;
  await mkdir("tmp/qa-captures", { recursive: true });
  const viewport = page.viewportSize();
  const prefix = `tmp/qa-captures/${test.info().project.name}-${name}`;
  await page.screenshot({ path: `${prefix}-desktop.png` });
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: `${prefix}-mobile.png` });
  } finally {
    if (viewport) await page.setViewportSize(viewport);
  }
}

async function loadedImage(image: Locator) {
  await expect(image).toBeVisible();
  await expect
    .poll(() =>
      image.evaluate(
        (element: HTMLImageElement) =>
          element.complete && element.naturalWidth > 0,
      ),
    )
    .toBe(true);
}

// Desktop Playwright projects have no touch screen, so a swipe is the pointer
// sequence a touch produces, delivered straight to the stage.
async function swipe(stage: Locator, from: number, to: number) {
  const touch = { pointerType: "touch", pointerId: 1, isPrimary: true };
  await stage.dispatchEvent("pointerdown", {
    ...touch,
    clientX: from,
    clientY: 300,
  });
  await stage.dispatchEvent("pointerup", {
    ...touch,
    clientX: to,
    clientY: 305,
  });
}

async function readAllPhotos(page: Page, albumID: string) {
  const entries: ViewerEntry[] = [];
  let cursor = "";
  do {
    const response = await page.request.get(
      `/api/albums/${albumID}/photos${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
    );
    expect(response.status()).toBe(200);
    const body = (await response.json()) as ViewerPage;
    entries.push(...body.entries);
    cursor = body.next_cursor;
  } while (cursor);
  return entries;
}

test("a member browses a published Album, opens photos by link, key, swipe and filmstrip, downloads one, and loses it all on revocation", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(240_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill("Alex");
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(
    page.getByRole("heading", { name: "Alex", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Google email address" })
    .fill("alex@example.test");
  await page.getByRole("button", { name: "Preauthorize email" }).click();
  await expect(
    page
      .getByRole("table", { name: "Preauthorizations", exact: true })
      .getByText("alex@example.test", { exact: true }),
  ).toBeVisible();

  await page.goto("/curator/import?q=Workbench - Browse");
  await page
    .getByRole("article")
    .filter({
      has: page.getByRole("heading", {
        name: "Workbench - Browse",
        exact: true,
      }),
    })
    .getByRole("button", { name: "Import", exact: true })
    .click();
  const outline = page.getByRole("navigation", { name: "Album outline" });
  await expect(
    outline.getByRole("link", { name: /Sunday, May 10, 2026/ }),
  ).toBeVisible({ timeout: 90_000 });
  const albumID = new URL(page.url()).pathname.split("/").pop()!;
  const viewerPath = `/albums/${albumID}/photos`;

  await outline
    .getByRole("link", { name: "Album access", exact: true })
    .click();
  const albumAccess = page.getByRole("form", {
    name: "Album access",
    exact: true,
  });
  await albumAccess.getByText(/not seen in this album/).click();
  const alexAlbum = albumAccess.getByRole("checkbox", {
    name: "Album access for Alex",
  });
  await alexAlbum.check();
  await albumAccess
    .getByRole("button", { name: "Save Album access", exact: true })
    .click();
  await expect(albumAccess.getByRole("status")).toHaveText(
    "Album access saved.",
  );
  await page
    .getByRole("button", { name: "Review & publish", exact: true })
    .click();
  const publication = page.getByRole("dialog", {
    name: "Ready to publish?",
    exact: true,
  });
  await publication
    .getByRole("button", { name: "Publish album", exact: true })
    .click();
  await expect(publication).toHaveCount(0);

  const memberContext = await browser.newContext({ baseURL });
  try {
    const member = await memberContext.newPage();
    await member.goto("/sign-in");
    await member
      .getByRole("textbox", { name: "Email", exact: true })
      .fill("alex@example.test");
    await member.getByRole("button", { name: "Sign in", exact: true }).click();
    await finishOnboarding(member);

    // Album list: cover, counts, and date range on the card.
    const card = member.getByRole("link", { name: /Workbench - Browse/ });
    await expect(card).toBeVisible();
    await expect(card).toContainText("221 photos, 0 videos");
    await expect(card).toContainText("May 10 to June 4, 2026");
    await loadedImage(card.getByRole("img", { name: "Workbench - Browse" }));
    await card.click();
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));

    // Photos tab: day headings with counts, no clock times, every page loaded.
    const tabs = member.getByRole("navigation", { name: "Album media" });
    await expect(
      tabs.getByRole("link", { name: "Photos 221", exact: true }),
    ).toHaveAttribute("aria-current", "page");
    await expect(
      member.getByRole("heading", { name: "Sunday, May 10, 2026 80 photos" }),
    ).toBeVisible();
    // Every day arrives in the background; slow engines need a moment.
    await expect(
      member.getByRole("link", { name: "Open photo browse-220" }),
    ).toBeAttached({ timeout: 30_000 });
    await expect(
      member.getByRole("link", { name: "Open photo coast-07" }),
    ).toBeAttached();
    await expect(member.getByText(/\d:\d\d [AP]M/)).toHaveCount(0);

    // The timeline stands in for the scrollbar: hovering names the month and
    // clicking near its end jumps down the page.
    const timeline = member.getByRole("slider", { name: "Timeline" });
    await expect(timeline).toBeVisible();
    await timeline.hover({ position: { x: 24, y: 40 } });
    await expect(timeline.getByText("May 2026")).toBeVisible();
    const rail = (await timeline.boundingBox())!;
    await member.mouse.click(rail.x + 24, rail.y + rail.height - 4);
    expect(await member.evaluate(() => window.scrollY)).toBeGreaterThan(0);
    await member.evaluate(() => window.scrollTo({ top: 0 }));

    // On a phone the rail gives way to a handle that appears while scrolling
    // and scrubs from where it is grabbed.
    await member.setViewportSize({ width: 390, height: 900 });
    await member.evaluate(() => window.scrollTo({ top: 300 }));
    const handle = timeline.locator("span.pointer-events-auto");
    const grip = (await handle.boundingBox())!;
    const centre = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };
    await member.mouse.move(centre.x, centre.y);
    await member.mouse.down();
    expect(await member.evaluate(() => window.scrollY)).toBe(300);
    await member.mouse.move(centre.x, centre.y + 200, { steps: 8 });
    await member.mouse.up();
    expect(await member.evaluate(() => window.scrollY)).toBeGreaterThan(1000);
    await member.evaluate(() => window.scrollTo({ top: 0 }));
    await captureLayouts(member, "browse-album");
    for (const width of [1440, 390]) {
      await member.setViewportSize({ width, height: 900 });
      expect(
        await member.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
    }
    await member.setViewportSize({ width: 1280, height: 900 });

    // The photo-only fixture has a truthful empty Videos tab.
    await tabs.getByRole("link", { name: "Videos 0", exact: true }).click();
    await expect(
      member.getByRole("heading", { name: "No videos in this album" }),
    ).toBeVisible();
    await member
      .getByRole("link", { name: "View photos", exact: true })
      .click();
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));

    // Reload a stable link to a photo on the second page.
    const photos = await readAllPhotos(member, albumID);
    expect(photos).toHaveLength(221);
    const target = photos[150];
    expect(target.title).toBe("browse-151");
    await member.goto(`${viewerPath}/${target.id}`);
    const dialog = member.getByRole("dialog");
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await loadedImage(dialog.getByRole("img", { name: "browse-151" }));
    await expect(dialog).toContainText("Workbench - Browse");
    await expect(dialog).toContainText("Monday, May 11, 2026");
    await captureLayouts(member, "browse-lightbox");

    // Zoom changes the rendered photo, and dragging pans without navigating.
    const zoom = dialog.getByRole("group", { name: "Photo zoom", exact: true });
    const photo = zoom.getByRole("img");
    const actions = dialog.getByRole("group", {
      name: "Photo actions",
      exact: true,
    });
    const zoomIn = actions.getByRole("button", {
      name: "Zoom in",
      exact: true,
    });
    const resetZoom = actions.getByRole("button", {
      name: "Reset zoom",
      exact: true,
    });
    const fitted = (await photo.boundingBox())!;
    await expect(zoomIn).toBeEnabled();
    await expect(
      actions.getByRole("link", { name: "Download photo" }),
    ).toBeVisible();
    expect((await zoomIn.boundingBox())!.y).toBeLessThan(
      (await zoom.boundingBox())!.y,
    );
    await expect(zoom).toHaveCSS("cursor", "default");
    await zoomIn.click();
    await expect(resetZoom).toBeVisible();
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo(fitted.width * 2);
    await resetZoom.click();
    await expect(zoomIn).toBeVisible();
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo(fitted.width);

    await zoom.dblclick();
    await expect(resetZoom).toBeVisible();
    const enlarged = (await photo.boundingBox())!;
    expect(enlarged.width).toBeCloseTo(fitted.width * 2);
    const viewport = (await zoom.boundingBox())!;
    const start = {
      x: viewport.x + viewport.width / 2,
      y: viewport.y + viewport.height / 2,
    };
    await member.mouse.move(start.x, start.y);
    await member.mouse.down();
    await member.mouse.move(start.x + 90, start.y + 70, { steps: 8 });
    await member.mouse.up();
    await expect
      .poll(async () => {
        const panned = (await photo.boundingBox())!;
        return Math.hypot(panned.x - enlarged.x, panned.y - enlarged.y);
      })
      .toBeGreaterThan(30);
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await resetZoom.click();

    await dialog.focus();
    await zoom.hover();
    await member.mouse.wheel(0, -200);
    await expect(resetZoom).toBeVisible();
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeGreaterThan(fitted.width);
    await expect(zoom).toBeFocused();
    await swipe(
      dialog.getByRole("group", { name: "Photo stage", exact: true }),
      400,
      200,
    );
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await member.keyboard.press("0");
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo(fitted.width);

    // Arrow keys still browse while zoomed, including when the photo has focus.
    await zoomIn.click();
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 152 of 221");
    await expect(resetZoom).toHaveCount(0);
    await expect(zoomIn).toBeEnabled();
    await zoomIn.click();
    await member.keyboard.press("ArrowLeft");
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await expect(resetZoom).toHaveCount(0);
    await expect(zoomIn).toBeEnabled();

    // Phone zoom stays inside the stage without covering navigation controls.
    await member.setViewportSize({ width: 390, height: 844 });
    await zoomIn.click();
    await expect(resetZoom).toBeVisible();
    await expect(zoom).toHaveCSS("overflow-x", "hidden");
    await expect(zoom).toHaveCSS("overflow-y", "hidden");
    expect((await photo.boundingBox())!.width).toBeGreaterThan(
      (await zoom.boundingBox())!.width,
    );
    expect(
      await member.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    for (const name of [
      "Reset zoom",
      "Previous photo",
      "Next photo",
      "Close photo",
    ]) {
      const control = dialog.getByRole("button", { name, exact: true });
      await expect(control).toBeInViewport({ ratio: 1 });
      await control.click({ trial: true });
    }

    // Stepping while zoomed opens a fitted photo. Return to 151 for the keys.
    await dialog
      .getByRole("button", { name: "Next photo", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 152 of 221");
    await loadedImage(zoom.getByRole("img", { name: "browse-152" }));
    await expect(zoomIn).toBeEnabled();
    await expect(resetZoom).toHaveCount(0);
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo((await zoom.boundingBox())!.width);
    await dialog
      .getByRole("button", { name: "Previous photo", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await loadedImage(zoom.getByRole("img", { name: "browse-151" }));
    await expect(zoomIn).toBeEnabled();
    await member.setViewportSize({ width: 1280, height: 900 });

    // Keys, swipe, and the filmstrip move through the page boundary.
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 152 of 221");
    await expect(member).toHaveURL(
      new RegExp(`${viewerPath}/${photos[151].id}$`),
    );
    await member.keyboard.press("ArrowLeft");
    await member.keyboard.press("ArrowLeft");
    await expect(dialog).toHaveAccessibleName("Photo 150 of 221");
    const stage = dialog.getByRole("group", {
      name: "Photo zoom",
      exact: true,
    });
    await swipe(stage, 400, 200);
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await swipe(stage, 200, 420);
    await expect(dialog).toHaveAccessibleName("Photo 150 of 221");
    const filmstrip = dialog.getByRole("navigation", {
      name: "Photos filmstrip",
    });
    await expect(
      filmstrip.getByRole("button", { name: "Go to photo 150", exact: true }),
    ).toBeInViewport();
    await filmstrip
      .getByRole("button", { name: "Go to photo 100", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 100 of 221");
    await expect(
      filmstrip.getByRole("button", { name: "Go to photo 100", exact: true }),
    ).toBeInViewport();
    await member
      .getByRole("button", { name: "Next photo", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 101 of 221");
    await expect(member).toHaveURL(
      new RegExp(`${viewerPath}/${photos[100].id}$`),
    );

    // Download the original as the authorized member.
    const downloadURL = photos[100].download_url;
    expect(downloadURL).toContain(`/entries/${photos[100].id}/original?v=`);
    const [download] = await Promise.all([
      member.waitForEvent("download"),
      member.getByRole("link", { name: "Download photo", exact: true }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("browse-101.jpg");
    expect(await download.failure()).toBeNull();
    const original = await member.request.get(downloadURL);
    expect(original.status()).toBe(200);
    expect(original.headers()["cache-control"]).toBe("private, no-store");
    expect(original.headers()["content-disposition"]).toContain("attachment");
    expect((await original.body()).byteLength).toBeGreaterThan(0);

    // Closing a directly opened photo returns to the gallery and focuses it;
    // opening from the grid returns focus to that grid item and steps history
    // back to the gallery.
    await member.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    await expect(
      member.getByRole("link", { name: "Open photo browse-101" }),
    ).toBeFocused();
    const opener = member.getByRole("link", { name: "Open photo browse-003" });
    await opener.click();
    await expect(dialog).toHaveAccessibleName("Photo 3 of 221");
    await loadedImage(dialog.getByRole("img", { name: "browse-003" }));
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 4 of 221");
    await member.goBack();
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    await expect(opener).toBeFocused();
    await member.goForward();
    await expect(dialog).toHaveAccessibleName("Photo 4 of 221");
    await member
      .getByRole("button", { name: "Close photo", exact: true })
      .click();
    await expect(dialog).toHaveCount(0);
    await expect(opener).toBeFocused();

    // The Curator's preview navigates the same way without downloads.
    await outline
      .getByRole("link", { name: "Viewer preview", exact: true })
      .click();
    await page
      .getByRole("link", { name: "Open photo browse-101", exact: true })
      .click();
    const preview = page.getByRole("dialog");
    await expect(preview).toHaveAccessibleName("Photo 101 of 221");
    const notice = preview.getByText("Previewing as Alex. Read only.");
    await expect(notice).toBeVisible();
    for (const width of [1280, 768]) {
      await page.setViewportSize({ width, height: 900 });
      const close = (await preview
        .getByRole("button", { name: "Close photo" })
        .boundingBox())!;
      const message = (await notice.boundingBox())!;
      expect(
        Math.abs(message.y + message.height / 2 - close.y - close.height / 2),
      ).toBeLessThan(1);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    const close = (await preview
      .getByRole("button", { name: "Close photo" })
      .boundingBox())!;
    expect((await notice.boundingBox())!.y).toBeGreaterThanOrEqual(
      close.y + close.height,
    );
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(
      preview.getByRole("link", { name: "Download photo" }),
    ).toHaveCount(0);
    await captureLayouts(page, "browse-preview-lightbox");
    await page.keyboard.press("ArrowRight");
    await expect(preview).toHaveAccessibleName("Photo 102 of 221");
    expect(new URL(page.url()).searchParams.get("entry")).toBe(photos[101].id);
    await page.keyboard.press("Escape");
    await expect(preview).toHaveCount(0);

    // Revoke Alex's access. New uncached requests fail neutrally, including
    // the stable link, the preview image, and the download.
    await outline
      .getByRole("link", { name: "Album access", exact: true })
      .click();
    await albumAccess.getByText(/not seen in this album/).click();
    await alexAlbum.uncheck();
    await albumAccess
      .getByRole("button", { name: "Save Album access", exact: true })
      .click();
    await expect(albumAccess.getByRole("status")).toHaveText(
      "Album access saved.",
    );
    await member.goto(`${viewerPath}/${target.id}`);
    await expect(
      member.getByRole("heading", { name: "Album not available", exact: true }),
    ).toBeVisible();
    await expect(member.getByRole("dialog")).toHaveCount(0);
    // APIRequestContext bypasses the browser's immutable image cache.
    expect((await member.request.get(target.preview_url)).status()).toBe(404);
    expect((await member.request.get(downloadURL)).status()).toBe(404);
    const albums = (await (
      await member.request.get("/api/albums")
    ).json()) as ViewerAlbum[];
    expect(albums).toEqual([]);
  } finally {
    await memberContext.close();
  }
});
