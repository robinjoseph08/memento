import type { Page } from "@playwright/test";

import type {
  AlbumDetail,
  Entry,
  ViewerEntry,
  ViewerPage,
} from "../app/types/generated/publishing";
import { expect, test } from "./fixtures";

async function readAllVideos(page: Page, albumID: string) {
  const entries: ViewerEntry[] = [];
  let cursor = "";
  do {
    const response = await page.request.get(
      `/api/albums/${albumID}/videos${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
    );
    expect(response.status()).toBe(200);
    const body = (await response.json()) as ViewerPage;
    entries.push(...body.entries);
    cursor = body.next_cursor;
  } while (cursor);
  return entries;
}

async function curatorEntries(page: Page, albumID: string) {
  const response = await page.request.get(`/api/curator/albums/${albumID}`);
  expect(response.status()).toBe(200);
  const album = (await response.json()) as AlbumDetail;
  return new Map(
    album.moments
      .flatMap((moment) => moment.entries)
      .map((entry): [string, Entry] => [entry.filename, entry]),
  );
}

// Playback, seeking, and chapter selection are read through the element so the
// journey holds in every engine, decoded or not.
async function currentTime(page: Page) {
  return page
    .getByRole("dialog")
    .locator("video")
    .evaluate((video: HTMLVideoElement) => video.currentTime);
}

test("videos play with titles, chapters, ranges, downloads, and recover a failed extraction", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(300_000);
  await immich.online();
  // Two clips cannot be probed at first: coast-retry recovers after this
  // checkpoint opens, coast-broken never does.
  await immich.checkpoint("chapter-probe", "fail");
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
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

  await page.goto("/curator/import?q=Workbench - Videos");
  await page
    .getByRole("article")
    .filter({
      has: page.getByRole("heading", {
        name: "Workbench - Videos",
        exact: true,
      }),
    })
    .getByRole("button", { name: "Import", exact: true })
    .click();
  const outline = page.getByRole("navigation", { name: "Album outline" });
  await expect(
    outline.getByRole("link", { name: /Wednesday, May 20, 2026/ }),
  ).toBeVisible({ timeout: 90_000 });
  const albumID = new URL(page.url()).pathname.split("/").pop()!;

  // Import queues extraction for every clip; the worker probes them one at a
  // time. Chaptered, unchaptered, and failed results all arrive without a
  // Curator action, and failures retry before they are shown as failed.
  await expect
    .poll(
      async () => {
        const entries = await curatorEntries(page, albumID);
        return [
          entries.get("birthday-party.webm")?.chapter_status,
          entries.get("clip-001.webm")?.chapter_status,
          entries.get("coast-retry.webm")?.chapter_status,
          entries.get("coast-broken.webm")?.chapter_status,
        ];
      },
      { timeout: 180_000, intervals: [1000] },
    )
    .toEqual(["complete", "complete", "failed", "failed"]);
  const entries = await curatorEntries(page, albumID);
  expect(
    entries.get("birthday-party.webm")?.chapters.map((c) => c.title),
  ).toEqual(["Arrival", "Cake", "Goodbyes"]);
  expect(entries.get("clip-001.webm")?.chapters).toEqual([]);

  // Give the chaptered clip a Memento title. The filename is the fallback.
  const moment = page.getByRole("region", {
    name: "Wednesday, May 20, 2026",
    exact: true,
  });
  await expect(
    moment.getByRole("img", { name: "Chapter extraction failed" }),
  ).toHaveCount(2);
  // The tile opens everything about the video: title, chapters, and access.
  await moment
    .getByRole("button", { name: "Edit birthday-party.webm", exact: true })
    .click();
  const details = page.getByRole("dialog", { name: "Video details" });
  await expect(
    details.getByRole("combobox", { name: "Access for Alex" }),
  ).toBeVisible();
  const titleField = details.getByRole("textbox", { name: "Video title" });
  await expect(titleField).toHaveValue("");
  await expect(titleField).toHaveAttribute("placeholder", "birthday-party");
  await expect(details.getByText("Arrival")).toBeVisible();
  await expect(details.getByText("Goodbyes")).toBeVisible();
  await titleField.fill("Birthday party");
  await details
    .getByRole("button", { name: "Save title", exact: true })
    .click();
  await expect(details).toHaveCount(0);

  // Retry the recoverable failure without blocking anything else.
  await moment
    .getByRole("button", { name: "Edit coast-retry.webm", exact: true })
    .click();
  await expect(details.getByRole("alert")).toContainText(
    "Chapter extraction failed",
  );
  await immich.checkpoint("chapter-probe", "open");
  await details
    .getByRole("button", { name: "Retry chapters", exact: true })
    .click();
  await expect(details.getByRole("status")).toContainText(
    "Reading chapters from the video",
  );
  await expect(details.getByText("This video has no chapters.")).toBeVisible({
    timeout: 60_000,
  });
  await page.keyboard.press("Escape");
  await expect(details).toHaveCount(0);
  await expect(
    moment.getByRole("img", { name: "Chapter extraction failed" }),
  ).toHaveCount(1);

  // Share with Alex and publish. Failed extraction never blocks publication.
  await outline
    .getByRole("link", { name: "Album access", exact: true })
    .click();
  const albumAccess = page.getByRole("form", {
    name: "Album access",
    exact: true,
  });
  await albumAccess.getByText(/not seen in this album/).click();
  await albumAccess
    .getByRole("checkbox", { name: "Album access for Alex" })
    .check();
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
    await member.getByRole("link", { name: /Workbench - Videos/ }).click();
    const viewerPath = `/albums/${albumID}/videos`;
    await member
      .getByRole("navigation", { name: "Album media" })
      .getByRole("link", { name: "Videos 103", exact: true })
      .click();
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    await expect(
      member.getByRole("heading", {
        name: "Wednesday, May 20, 2026 63 videos",
      }),
    ).toBeVisible();
    const opener = member.getByRole("link", {
      name: "Open video Birthday party",
      exact: true,
    });
    await expect(opener).toBeVisible();
    await expect(member.locator("video")).toHaveCount(0);

    // Open the chaptered video: title, player, chapter overlay, seeking.
    await opener.click();
    const dialog = member.getByRole("dialog");
    await expect(dialog).toHaveAccessibleName("Video 1 of 103");
    await expect(member).toHaveURL(/\/videos\/[^/]+$/);
    const video = dialog.locator("video");
    await expect(video).toHaveCount(1);
    const playbackURL = await video.getAttribute("src");
    expect(playbackURL).toContain("/playback?v=");
    await expect(dialog).toContainText("Birthday party");
    // The chapter picker under the player names the playing chapter and
    // follows the video; choosing one seeks to its start.
    const picker = dialog.getByRole("combobox", { name: "Chapter" });
    await expect(picker).toHaveText(/Arrival/);
    await picker.click();
    await member.getByRole("option", { name: /Cake/ }).click();
    await expect(member.getByRole("option")).toHaveCount(0);
    await expect.poll(() => currentTime(member)).toBeGreaterThanOrEqual(1.9);
    await expect(picker).toHaveText(/Cake/);
    await video.evaluate((element: HTMLVideoElement) => {
      element.currentTime = 5;
    });
    await expect.poll(() => currentTime(member)).toBeGreaterThanOrEqual(4.9);
    await expect(picker).toHaveText(/Goodbyes/);
    await expect(dialog).toHaveAccessibleName("Video 1 of 103");

    // Playback proxies byte ranges with Memento's validators.
    const partial = await member.request.get(playbackURL!, {
      headers: { Range: "bytes=0-99" },
    });
    expect(partial.status()).toBe(206);
    expect(partial.headers()["content-range"]).toMatch(/^bytes 0-99\/\d+$/);
    expect(partial.headers()["cache-control"]).toBe(
      "private, max-age=31536000, immutable",
    );
    expect(partial.headers()["accept-ranges"]).toBe("bytes");
    expect((await partial.body()).byteLength).toBe(100);

    // Download the original as the authorized member.
    const [download] = await Promise.all([
      member.waitForEvent("download"),
      dialog.getByRole("link", { name: "Download video", exact: true }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("birthday-party.webm");
    expect(await download.failure()).toBeNull();

    // Neighbours: a plain clip says so quietly; the broken one is honest too.
    await member.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAccessibleName("Video 2 of 103");
    await expect(
      dialog.getByText("No chapters", { exact: true }),
    ).toBeVisible();
    await expect(dialog.getByRole("combobox", { name: "Chapter" })).toHaveCount(
      0,
    );
    await dialog
      .getByRole("button", { name: "Next video", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Video 3 of 103");
    await expect(
      dialog.getByText("Chapters unavailable", { exact: true }),
    ).toBeVisible();

    // Reload a stable link beyond the first page, then cross the boundary.
    const videos = await readAllVideos(member, albumID);
    expect(videos).toHaveLength(103);
    expect(videos[0].title).toBe("Birthday party");
    expect(videos[1].title).toBe("coast-retry");
    await member.goto(`${viewerPath}/${videos[100].id}`);
    await expect(dialog).toHaveAccessibleName("Video 101 of 103");
    await expect(dialog.locator("video")).toHaveAttribute(
      "src",
      videos[100].playback_url,
    );
    await member.keyboard.press("ArrowLeft");
    await expect(dialog).toHaveAccessibleName("Video 100 of 103");
    await expect(member).toHaveURL(
      new RegExp(`${viewerPath}/${videos[99].id}$`),
    );
    await dialog
      .getByRole("navigation", { name: "Videos in album" })
      .getByRole("button", { name: "Go to video 1", exact: true })
      .click();
    await expect(dialog).toHaveAccessibleName("Video 1 of 103");
    await member.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(new RegExp(`${viewerPath}$`));
    await expect(member.locator("video")).toHaveCount(0);
    await expect(opener).toBeFocused();

    // Curator preview plays the same video without a download.
    await outline
      .getByRole("link", { name: "Viewer preview", exact: true })
      .click();
    await page
      .getByRole("navigation", { name: "Album media" })
      .getByRole("link", { name: "Videos 103", exact: true })
      .click();
    await page
      .getByRole("link", { name: "Open video Birthday party", exact: true })
      .click();
    const preview = page.getByRole("dialog");
    await expect(preview).toHaveAccessibleName("Video 1 of 103");
    await expect(preview).toContainText("Previewing as Alex");
    await expect(preview.locator("video")).toHaveAttribute(
      "src",
      /\/api\/media\/preview\/[^/]+\/entries\/[^/]+\/playback\?v=/,
    );
    await expect(
      preview.getByRole("link", { name: "Download video" }),
    ).toHaveCount(0);
    await preview.getByRole("combobox", { name: "Chapter" }).click();
    await page.getByRole("option", { name: /Goodbyes/ }).click();
    await expect(page.getByRole("option")).toHaveCount(0);
    await expect.poll(() => currentTime(page)).toBeGreaterThanOrEqual(3.9);
    await page.keyboard.press("Escape");
    await expect(preview).toHaveCount(0);

    // Clearing the title restores the filename for the member.
    await outline
      .getByRole("link", { name: /Wednesday, May 20, 2026/ })
      .click();
    await moment
      .getByRole("button", { name: "Edit birthday-party.webm", exact: true })
      .click();
    await expect(titleField).toHaveValue("Birthday party");
    await titleField.fill("");
    await details
      .getByRole("button", { name: "Save title", exact: true })
      .click();
    await expect(details).toHaveCount(0);
    await member.reload();
    await expect(
      member.getByRole("link", {
        name: "Open video birthday-party",
        exact: true,
      }),
    ).toBeVisible();
  } finally {
    await memberContext.close();
  }
});
