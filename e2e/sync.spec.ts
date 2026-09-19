import type { Page } from "@playwright/test";

import type { AlbumDetail, Entry } from "../app/types/generated/publishing";
import { expect, finishOnboarding, test } from "./fixtures";

async function createPerson(page: Page, name: string, email: string) {
  await page.getByRole("link", { name: "People", exact: true }).click();
  await page.getByRole("button", { name: "Add person", exact: true }).click();
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Create person" }).click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await page.getByRole("textbox", { name: "Google email address" }).fill(email);
  await page.getByRole("button", { name: "Preauthorize email" }).click();
  await expect(
    page
      .getByRole("table", { name: "Preauthorizations", exact: true })
      .getByText(email, { exact: true }),
  ).toBeVisible();
}

async function signInAndOnboard(page: Page, email: string, name: string) {
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("textbox", { name: "Display name" }).fill(name);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await finishOnboarding(page);
}

// Imports one fixture Album, grants Album access to everyone named, and
// publishes it. Returns the Album ID.
async function importAndPublish(page: Page, title: string, people: string[]) {
  await page.goto(`/curator/import?q=${encodeURIComponent(title)}`);
  await page
    .getByRole("article")
    .filter({ has: page.getByRole("heading", { name: title, exact: true }) })
    .getByRole("button", { name: "Import", exact: true })
    .click();
  const outline = page.getByRole("navigation", { name: "Album outline" });
  await expect(
    outline.getByRole("link", { name: "Album access", exact: true }),
  ).toBeVisible({ timeout: 60_000 });
  const albumID = new URL(page.url()).pathname.split("/").pop()!;
  await outline
    .getByRole("link", { name: "Album access", exact: true })
    .click();
  const albumAccess = page.getByRole("form", {
    name: "Album access",
    exact: true,
  });
  await albumAccess.getByText(/not seen in this album/).click();
  for (const person of people) {
    await albumAccess
      .getByRole("checkbox", { name: `Album access for ${person}` })
      .check();
  }
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
  return albumID;
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

async function counts(page: Page, photos: number, videos: number) {
  const tabs = page.getByRole("navigation", { name: "Album media" });
  await expect(
    tabs.getByRole("link", { name: `Photos ${photos}`, exact: true }),
  ).toBeVisible();
  await expect(
    tabs.getByRole("link", { name: `Videos ${videos}`, exact: true }),
  ).toBeVisible();
}

test("Curator reviews Immich changes, cancels, places additions, replaces a cover, and applies them without announcing", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  test.setTimeout(300_000);
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);
  await createPerson(page, "Alex", "alex@example.test");
  const albumID = await importAndPublish(page, "Fixture Album - Coast", [
    "Alex",
  ]);
  const albumURL = `/curator/albums/${albumID}`;

  // Alex completes Onboarding after publication, so every current photo and
  // video is already announced to them.
  const alexContext = await browser.newContext({ baseURL });
  try {
    const alex = await alexContext.newPage();
    await signInAndOnboard(alex, "alex@example.test", "Alex");
    await alex.goto(`/albums/${albumID}`);
    await counts(alex, 4, 2);

    // Nothing has changed yet.
    const check = page.getByRole("button", {
      name: "Check for changes",
      exact: true,
    });
    await check.click();
    const dialog = page.getByRole("dialog", {
      name: "Changes in Immich",
      exact: true,
    });
    await expect(dialog).toContainText(
      "This album matches Immich. Nothing to apply.",
    );
    await expect(
      dialog.getByRole("button", { name: "Apply changes" }),
    ).toHaveCount(0);
    await dialog.getByRole("button", { name: "Close", exact: true }).click();
    await expect(dialog).toHaveCount(0);

    // The photographer edits Immich: coast-03 (the June 2 cover) leaves, a
    // chaptered video and a June 4 photo arrive, coast-05 is replaced, and
    // the description changes.
    const before = await curatorEntries(page, albumID);
    await immich.library({
      album: "fixture-album-coast",
      members: [
        "fixture-asset-01",
        "fixture-asset-02",
        "fixture-asset-04",
        "fixture-asset-05",
        "fixture-asset-06",
        "workbench-video-party",
        "workbench-asset-001",
      ],
      description: "Edited in Immich",
      assets: {
        "fixture-asset-05": { checksum: "AAAAAAAAAAAAAAAAAAAAAAAAAAA=" },
      },
    });

    // Review, then cancel: nothing changes and a later check recomputes.
    await check.click();
    await expect(
      dialog.getByRole("region", { name: "New in Immich" }),
    ).toContainText("birthday-party");
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    expect(await curatorEntries(page, albumID)).toEqual(before);
    await expect(
      page
        .getByRole("navigation", { name: "Album outline" })
        .getByRole("link", { name: /May 20, 2026/ }),
    ).toHaveCount(0);

    await check.click();
    const additions = dialog.getByRole("region", { name: "New in Immich" });
    await expect(additions).toContainText("birthday-party");
    await expect(additions).toContainText(
      "Photo taken June 4, 2026 at 12:00 PM",
    );
    await expect(
      additions.getByRole("combobox", {
        name: "Destination for birthday-party",
      }),
    ).toHaveText("New Moment: May 20, 2026");
    const photoDestination = additions.getByRole("combobox", {
      name: "Destination for Photo taken June 4, 2026 at 12:00 PM",
    });
    await expect(photoDestination).toHaveText("New Moment: June 4, 2026");
    // The Curator overrides one suggestion; the review recomputes for it.
    await photoDestination.click();
    await page
      .getByRole("option", { name: "June 3, 2026", exact: true })
      .click();
    await expect(photoDestination).toHaveText("June 3, 2026");
    const removals = dialog.getByRole("region", { name: "Removed in Immich" });
    await expect(removals).toContainText(
      "Photo taken June 2, 2026 at 12:00 AM",
    );
    await expect(removals).toContainText("was the cover");
    const changes = dialog.getByRole("region", { name: "Changed in Immich" });
    await expect(changes).toContainText("Photo taken June 3, 2026 at 10:00 AM");
    await expect(changes).toContainText("new file version");
    await expect(
      dialog.getByRole("region", { name: "Description change" }),
    ).toContainText("Edited in Immich");
    const visibility = dialog.getByRole("region", {
      name: "Visibility review",
    });
    await expect(visibility).toContainText("Alex");
    await expect(visibility).toContainText("gains 2");
    await expect(visibility).toContainText("loses 1");
    const apply = dialog.getByRole("button", { name: "Apply changes" });
    await expect(apply).toBeDisabled();
    await expect(dialog.getByRole("alert")).toContainText(
      "Choose a new cover for June 2, 2026.",
    );
    // The radio is visually hidden behind its thumbnail label.
    await removals
      .locator(
        'label:has(input[aria-label="Use Photo taken June 2, 2026 at 12:00 AM as the cover of June 2, 2026"])',
      )
      .click();
    await expect(
      removals.getByRole("radio", {
        name: "Use Photo taken June 2, 2026 at 12:00 AM as the cover of June 2, 2026",
      }),
    ).toBeChecked();
    await expect(apply).toBeEnabled();
    await apply.click();
    await expect(dialog).toHaveCount(0);

    // The Album now shows the approved structure and description.
    const outline = page.getByRole("navigation", { name: "Album outline" });
    await expect(
      outline.getByRole("link", { name: /Wednesday, May 20, 2026/ }),
    ).toBeVisible();
    await expect(
      outline.getByRole("link", { name: /Thursday, June 4, 2026/ }),
    ).toHaveCount(0);
    const detail = (await (
      await page.request.get(`/api/curator/albums/${albumID}`)
    ).json()) as AlbumDetail;
    expect(
      detail.moments.find((moment) =>
        moment.entries.some((entry) => entry.filename === "workbench-001.jpg"),
      )?.date,
    ).toBe("2026-06-03");
    await page.goto(`${albumURL}?section=details`);
    await expect(
      page.getByRole("textbox", { name: "Description from Immich" }),
    ).toHaveValue("Edited in Immich");
    const after = await curatorEntries(page, albumID);
    expect(after.has("coast-03.jpg")).toBe(false);
    expect(after.get("coast-01.jpg")?.thumbnail_url).toBe(
      before.get("coast-01.jpg")?.thumbnail_url,
    );
    expect(after.get("coast-05.jpg")?.thumbnail_url).not.toBe(
      before.get("coast-05.jpg")?.thumbnail_url,
    );
    expect(after.get("coast-05.jpg")?.thumbnail_url).toMatch(
      /^\/api\/media\/entries\/.+\?v=/,
    );

    // The new video is probed automatically.
    await expect
      .poll(
        async () =>
          (await curatorEntries(page, albumID)).get("birthday-party.webm")
            ?.chapter_status,
        { timeout: 180_000, intervals: [1000] },
      )
      .toBe("complete");
    expect(
      (await curatorEntries(page, albumID))
        .get("birthday-party.webm")
        ?.chapters.map((chapter) => chapter.title),
    ).toEqual(["Arrival", "Cake", "Goodbyes"]);

    // Alex sees the new media but has not been told about it.
    await alex.goto(`/albums/${albumID}`);
    await counts(alex, 4, 3);
    await expect(
      alex.getByRole("button", { name: "Updates", exact: true }),
    ).toBeVisible();
    await page.goto("/curator/updates");
    const updates = page.getByRole("form", {
      name: "Send updates",
      exact: true,
    });
    const alexRow = updates.getByRole("listitem").filter({ hasText: "Alex" });
    await expect(alexRow).toContainText("1 photo, 1 video");

    // A recheck finds nothing left to do.
    await page.goto(albumURL);
    await check.click();
    await expect(dialog).toContainText(
      "This album matches Immich. Nothing to apply.",
    );
    await dialog.getByRole("button", { name: "Close", exact: true }).click();

    // Keeping an existing photo out is a local decision: the June 1 Moment
    // loses its only item, the Excluded section lists it, and a recheck does
    // not offer it back.
    await outline.getByRole("link", { name: /Monday, June 1, 2026/ }).click();
    const juneFirst = page.getByRole("region", { name: "June 1, 2026" });
    await juneFirst
      .getByRole("button", { name: "Select", exact: true })
      .click();
    await juneFirst
      .getByRole("checkbox", {
        name: "Select Photo taken June 1, 2026 at 11:59 PM",
      })
      .check();
    await juneFirst.getByRole("button", { name: "Keep out" }).click();
    const keepOut = page.getByRole("dialog", {
      name: "Keep 1 item out of this album?",
    });
    await expect(keepOut).toContainText("loses its last item");
    await expect(
      keepOut.getByRole("region", { name: "Visibility review" }),
    ).toContainText("loses 1");
    await keepOut.getByRole("button", { name: "Keep out" }).click();
    await expect(keepOut).toHaveCount(0);
    await expect(
      outline.getByRole("link", { name: /Monday, June 1, 2026/ }),
    ).toHaveCount(0);
    const excludedLink = outline.getByRole("link", { name: /Excluded media/ });
    await expect(excludedLink).toContainText("1");
    await check.click();
    await expect(dialog).toContainText(
      "This album matches Immich. Nothing to apply.",
    );
    await dialog.getByRole("button", { name: "Close", exact: true }).click();
    await alex.goto(`/albums/${albumID}`);
    await counts(alex, 3, 3);

    // Add back returns it, with its identity, into a new Moment for its day.
    await excludedLink.click();
    const excludedList = page.getByRole("list", { name: "Excluded media" });
    await expect(excludedList).toContainText(
      "Photo taken June 1, 2026 at 11:59 PM",
    );
    await excludedList.getByRole("button", { name: "Add back" }).click();
    const addBack = page.getByRole("dialog", {
      name: "Add this photo back?",
    });
    await expect(addBack.getByRole("combobox", { name: "Moment" })).toHaveText(
      "New Moment: Jun 1",
    );
    await addBack.getByRole("button", { name: "Add back" }).click();
    await expect(addBack).toHaveCount(0);
    await expect(
      outline.getByRole("link", { name: /Monday, June 1, 2026/ }),
    ).toBeVisible();
    expect((await curatorEntries(page, albumID)).get("coast-01.jpg")?.id).toBe(
      before.get("coast-01.jpg")?.id,
    );
    await alex.goto(`/albums/${albumID}`);
    await counts(alex, 4, 3);
  } finally {
    await alexContext.close();
  }
});
