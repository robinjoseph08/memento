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

// A touch that drags down the stage, which closes the lightbox.
async function pullDown(stage: Locator, x: number, from: number, to: number) {
  const touch = { pointerType: "touch", pointerId: 1, isPrimary: true };
  await stage.dispatchEvent("pointerdown", {
    ...touch,
    clientX: x,
    clientY: from,
  });
  await stage.dispatchEvent("pointermove", {
    ...touch,
    clientX: x,
    clientY: (from + to) / 2,
  });
  await stage.dispatchEvent("pointerup", { ...touch, clientX: x, clientY: to });
}

// A touch that lands and lifts in place.
async function tap(stage: Locator, x: number, y: number) {
  const touch = { pointerType: "touch", pointerId: 1, isPrimary: true };
  await stage.dispatchEvent("pointerdown", {
    ...touch,
    clientX: x,
    clientY: y,
  });
  await stage.dispatchEvent("pointerup", { ...touch, clientX: x, clientY: y });
}

// How visible an element is once its ancestors' fading is counted in.
function opacity(locator: Locator) {
  return locator.evaluate((element) => {
    let value = 1;
    for (let node: Element | null = element; node; node = node.parentElement)
      value *= Number(getComputedStyle(node).opacity);
    return value;
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
    .getByRole("textbox", { name: "Email address" })
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
    const photos = await readAllPhotos(member, albumID);
    expect(photos).toHaveLength(221);
    await expect(
      member.locator(`[data-entry-id="${photos[219].id}"]`),
    ).toBeAttached({ timeout: 30_000 });
    await expect(
      member.locator(`[data-entry-id="${photos[220].id}"]`),
    ).toBeAttached();
    await expect(member.getByText(/\d:\d\d [AP]M/)).toHaveCount(0);

    // The timeline stands in for the scrollbar: this album spans a few weeks,
    // so hovering names the day, and clicking near its end jumps down the page.
    const timeline = member.getByRole("slider", { name: "Timeline" });
    await expect(timeline).toBeVisible();
    await timeline.hover({ position: { x: 24, y: 40 } });
    await expect(timeline.getByText(/^May \d+, 2026$/)).toBeVisible();
    const rail = (await timeline.boundingBox())!;
    await member.mouse.click(rail.x + 24, rail.y + rail.height - 4);
    expect(await member.evaluate(() => window.scrollY)).toBeGreaterThan(0);
    await member.evaluate(() => window.scrollTo({ top: 0 }));

    // On a phone the rail gives way to a handle that appears while scrolling
    // and scrubs from where it is grabbed.
    await member.setViewportSize({ width: 390, height: 900 });
    await member.evaluate(() => window.scrollTo({ top: 300 }));
    const handle = timeline.locator("span.pointer-events-auto");
    // The handle follows the page once it has seen the scroll, which WebKit
    // reports a frame later, so it is grabbed only after it stops moving.
    let grip = { x: 0, y: -1, width: 0, height: 0 };
    await expect
      .poll(async () => {
        const box = await handle.boundingBox();
        const settled = !!box && box.y === grip.y;
        if (box) grip = box;
        return settled;
      })
      .toBe(true);
    const centre = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };
    await member.mouse.move(centre.x, centre.y);
    await member.mouse.down();
    expect(await member.evaluate(() => window.scrollY)).toBe(300);
    await member.mouse.move(centre.x, centre.y + 200, { steps: 8 });
    await member.mouse.up();
    await expect
      .poll(() => member.evaluate(() => window.scrollY))
      .toBeGreaterThan(1000);
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
    const target = photos[150];
    expect(target.title).toBe("");
    await member.goto(`${viewerPath}/${target.id}`);
    const dialog = member.getByRole("dialog");
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await loadedImage(
      dialog
        .getByRole("group", { name: "Photo zoom", exact: true })
        .getByRole("img", {
          name: "Photo taken May 11, 2026 at 9:02 AM",
        }),
    );
    await expect(dialog).toContainText("Workbench - Browse");
    await expect(dialog).toContainText("Mon, May 11, 2026");
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
    // The photo fills the window and its controls float over it.
    expect((await zoom.boundingBox())!.height).toBe(
      member.viewportSize()!.height,
    );
    const header = (await dialog.locator("header").boundingBox())!;
    expect(header.y).toBe((await zoom.boundingBox())!.y);
    expect((await zoomIn.boundingBox())!.y).toBeLessThan(
      header.y + header.height,
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

    // A double click glides to the new zoom rather than jumping.
    await zoom.dblclick();
    await expect(resetZoom).toBeVisible();
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo(fitted.width * 2);
    const enlarged = (await photo.boundingBox())!;
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
    const stageWidth = (await zoom.boundingBox())!.width;
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeGreaterThan(stageWidth);
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
    await loadedImage(
      zoom.getByRole("img", {
        name: "Photo taken May 11, 2026 at 9:02 AM",
      }),
    );
    await expect(zoomIn).toBeEnabled();
    await expect(resetZoom).toHaveCount(0);
    await expect
      .poll(async () => (await photo.boundingBox())!.width)
      .toBeCloseTo((await zoom.boundingBox())!.width);
    await dialog
      .getByRole("button", { name: "Previous photo", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await loadedImage(
      zoom.getByRole("img", {
        name: "Photo taken May 11, 2026 at 9:02 AM",
      }),
    );
    await expect(zoomIn).toBeEnabled();
    await member.setViewportSize({ width: 1280, height: 900 });

    // Clicking the photo focuses it for its zoom keys, and the arrow keys
    // keep working after it steps out of view.
    await dialog
      .getByRole("group", { name: "Photo zoom", exact: true })
      .click({ position: { x: 200, y: 200 } });
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 152 of 221");
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 153 of 221");
    await member.keyboard.press("ArrowLeft");
    await member.keyboard.press("ArrowLeft");
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");

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
    // A swiped photo arrives as the image already on screen beside the open
    // one, not a new one that flashes black while it decodes.
    await stage.evaluate((element) => {
      for (const image of element
        .closest("[role=dialog]")!
        .querySelectorAll("img"))
        image.dataset.seen = "true";
    });
    await swipe(stage, 400, 200);
    await expect(dialog).toHaveAccessibleName("Photo 151 of 221");
    await expect(stage.getByRole("img")).toHaveAttribute("data-seen", "true");
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

    // The controls fade while the photo is just being looked at. A tap in
    // the middle brings them back and another hides them; a tap on either
    // side steps without them.
    const photoStage = dialog.getByRole("group", {
      name: "Photo stage",
      exact: true,
    });
    const closeButton = dialog.getByRole("button", {
      name: "Close photo",
      exact: true,
    });
    const size = member.viewportSize()!;
    await member.mouse.move(size.width / 2, size.height / 2);
    await expect.poll(() => opacity(closeButton), { timeout: 6000 }).toBe(0);
    await tap(photoStage, size.width / 2, size.height / 2);
    await expect.poll(() => opacity(closeButton)).toBe(1);
    await tap(photoStage, size.width / 2, size.height / 2);
    await expect.poll(() => opacity(closeButton)).toBe(0);
    await tap(photoStage, size.width - 20, size.height / 2);
    await expect(dialog).toHaveAccessibleName("Photo 102 of 221");
    await tap(photoStage, 20, size.height / 2);
    await expect(dialog).toHaveAccessibleName("Photo 101 of 221");
    expect(await opacity(closeButton)).toBe(0);
    await member.mouse.move(size.width / 2 + 10, size.height / 2);
    await expect.poll(() => opacity(closeButton)).toBe(1);

    // Download the original as the authorized member.
    const downloadURL = photos[100].download_url;
    expect(downloadURL).toContain(`/entries/${photos[100].id}/original?v=`);
    const [download] = await Promise.all([
      member.waitForEvent("download"),
      member.getByRole("link", { name: "Download photo", exact: true }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("photo-2026-05-11-090139.jpg");
    expect(await download.failure()).toBeNull();
    const original = await member.request.get(downloadURL);
    expect(original.status()).toBe(200);
    expect(original.headers()["cache-control"]).toBe("private, no-store");
    expect(original.headers()["content-disposition"]).toContain("attachment");
    expect((await original.body()).byteLength).toBeGreaterThan(0);

    // Closing returns to the gallery at the photo that was open last and
    // focuses it there. Opening from the grid steps history back to the
    // gallery; a direct link replaces the lightbox with it.
    await member.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    const directlyOpened = member.locator(
      `[data-entry-id="${photos[100].id}"]`,
    );
    await expect(directlyOpened).toBeFocused();
    await expect(directlyOpened).toBeInViewport();
    const opener = member.locator(`[data-entry-id="${photos[2].id}"]`);
    await opener.click();
    await expect(dialog).toHaveAccessibleName("Photo 3 of 221");
    await loadedImage(
      dialog
        .getByRole("group", { name: "Photo zoom", exact: true })
        .getByRole("img", {
          name: "Photo taken May 10, 2026 at 9:00 AM",
        }),
    );
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Photo 4 of 221");
    await member.goBack();
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    await expect(
      member.locator(`[data-entry-id="${photos[3].id}"]`),
    ).toBeFocused();
    await member.goForward();
    await expect(dialog).toHaveAccessibleName("Photo 4 of 221");
    await dialog
      .getByRole("navigation", { name: "Photos filmstrip" })
      .getByRole("button", { name: "Go to photo 80", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Photo 80 of 221");
    // Pulling the photo down closes it like the close button.
    await pullDown(photoStage, 600, 300, 500);
    await expect(dialog).toHaveCount(0);
    const lastOpen = member.locator(`[data-entry-id="${photos[79].id}"]`);
    await expect(lastOpen).toBeFocused();
    await expect(lastOpen).toBeInViewport();

    // The Curator's preview navigates the same way without downloads.
    await outline
      .getByRole("link", { name: "Viewer preview", exact: true })
      .click();
    await page.locator(`[data-entry-id="${photos[100].id}"]`).click();
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
