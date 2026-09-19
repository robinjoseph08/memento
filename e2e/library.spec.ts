import { mkdir } from "node:fs/promises";

import type { Person } from "../app/types/generated/identity";
import type {
  AlbumDetail,
  PublicationReview,
} from "../app/types/generated/publishing";
import { expect, finishOnboarding, playbackSource, test } from "./fixtures";

test("a viewer browses photos and videos across albums, newest first without duplicates", async ({
  page,
  browser,
  baseURL,
  immich,
}) => {
  await immich.online();
  await page.goto("/setup");
  await page.getByRole("button", { name: "Claim installation" }).click();
  await finishOnboarding(page);

  const headers = { Origin: new URL(page.url()).origin };
  const personResponse = await page.request.post("/api/people", {
    headers,
    data: { display_name: "Alex" },
  });
  expect(personResponse.ok()).toBe(true);
  const person = (await personResponse.json()) as Person;
  expect(
    (
      await page.request.post(`/api/people/${person.id}/preauthorizations`, {
        headers,
        data: { email: "alex@example.test" },
      })
    ).ok(),
  ).toBe(true);

  const albums: AlbumDetail[] = [];
  for (const source of ["fixture-album-coast", "fixture-album-family"]) {
    const imported = await page.request.post("/api/curator/imports", {
      headers,
      data: { source_id: source },
    });
    expect(imported.ok()).toBe(true);
    const album = (await imported.json()) as AlbumDetail;
    const url = `/api/curator/albums/${album.id}`;
    await expect
      .poll(async () => {
        const result = (await (
          await page.request.get(url)
        ).json()) as AlbumDetail;
        return result.status;
      })
      .toBe("complete");
    expect(
      (
        await page.request.post(`${url}/access`, {
          headers,
          data: { people: [{ person_id: person.id, allowed: true }] },
        })
      ).ok(),
    ).toBe(true);
    const reviewResponse = await page.request.get(`${url}/publication`);
    expect(reviewResponse.ok()).toBe(true);
    const review = (await reviewResponse.json()) as PublicationReview;
    expect(
      (
        await page.request.post(`${url}/publish`, {
          headers,
          data: { review_token: review.review_token },
        })
      ).ok(),
    ).toBe(true);
    albums.push(album);
  }

  const memberContext = await browser.newContext({ baseURL });
  try {
    const member = await memberContext.newPage();
    await member.goto("/sign-in");
    await member
      .getByRole("textbox", { name: "Email", exact: true })
      .fill("alex@example.test");
    await member.getByRole("button", { name: "Sign in", exact: true }).click();
    await finishOnboarding(member);
    await member
      .getByRole("navigation", { name: "Main navigation" })
      .getByRole("link", { name: "Library" })
      .click();
    await expect(member).toHaveURL(/\/library\/photos$/);
    await expect(member).toHaveTitle("Library | Memento");
    await expect(
      member.getByText(
        "You can view all of your photos and videos across all your albums.",
      ),
    ).toBeVisible();
    await expect(
      member
        .getByRole("navigation", { name: "Main navigation" })
        .getByRole("link"),
    ).toHaveText(["Albums", "Library"]);
    const photos = member.getByRole("link", { name: /^Open photo/ });
    await expect(photos).toHaveCount(5);
    await expect(photos.first()).toHaveAccessibleName(
      "Open photo taken June 4, 2026 at 12:00 PM",
    );
    await expect(photos.last()).toHaveAccessibleName(
      "Open photo taken June 1, 2026 at 11:59 PM",
    );
    await expect(
      member.getByRole("link", {
        name: "Open photo 1 taken June 2, 2026 at 12:00 AM",
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      member.getByRole("link", {
        name: "Open photo 2 taken June 2, 2026 at 12:00 AM",
        exact: true,
      }),
    ).toBeVisible();
    await expect(member.getByRole("heading", { level: 2 }).first()).toHaveText(
      "Thursday, June 4, 2026 1 photo",
    );

    await photos.first().click();
    const dialog = member.getByRole("dialog");
    await expect(
      dialog.getByRole("img", {
        name: "Photo taken June 4, 2026 at 12:00 PM",
        exact: true,
      }),
    ).toBeVisible();
    await member.reload();
    await expect(
      dialog.getByRole("img", {
        name: "Photo taken June 4, 2026 at 12:00 PM",
        exact: true,
      }),
    ).toBeVisible();
    await member.keyboard.press("ArrowRight");
    await expect(
      dialog.getByRole("img", {
        name: "Photo taken June 3, 2026 at 10:00 AM",
        exact: true,
      }),
    ).toBeVisible();
    await member.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(member).toHaveURL(/\/library\/photos$/);

    const tabs = member.getByRole("navigation", { name: "Library media" });
    await tabs.getByRole("link", { name: "Videos 2" }).click();
    const videos = member.getByRole("link", { name: /^Open video/ });
    await expect(videos).toHaveCount(2);
    await expect(videos.first()).toHaveAccessibleName("Open video coast-06");
    await expect(videos.last()).toHaveAccessibleName("Open video coast-04");
    await videos.first().click();
    await playbackSource(dialog.locator("video"));
    await member.keyboard.press("Escape");
    await tabs.getByRole("link", { name: "Photos 5" }).click();

    for (const width of [1440, 390]) {
      await member.setViewportSize({ width, height: 900 });
      await member.evaluate(() => window.scrollTo(0, 0));
      const libraryHeading = await member
        .getByRole("heading", { name: "Your library", exact: true })
        .boundingBox();
      await member.goto("/albums");
      const albumHeading = await member
        .getByRole("heading", { name: "Your albums", exact: true })
        .boundingBox();
      expect(libraryHeading?.y).toBe(albumHeading?.y);
      expect(libraryHeading?.height).toBe(albumHeading?.height);
      await member.goto("/library/photos");
      await expect(photos).toHaveCount(5);
      expect(
        await member.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBe(true);
      if (process.env.QA_CAPTURES === "1") {
        await mkdir("tmp/qa-captures", { recursive: true });
        await member.screenshot({
          path: `tmp/qa-captures/${test.info().project.name}-library-${width}.png`,
        });
      }
    }
    await member.getByRole("button", { name: "Open navigation" }).click();
    await member
      .getByRole("navigation", { name: "Mobile navigation" })
      .getByRole("link", { name: "Albums", exact: true })
      .click();
    await expect(member).toHaveURL(/\/albums$/);
    await member.goto(`/albums/${albums[0].id}/photos`);
    await expect(photos.first()).toHaveAccessibleName(
      "Open photo taken June 1, 2026 at 11:59 PM",
    );
    await expect(photos.last()).toHaveAccessibleName(
      "Open photo taken June 3, 2026 at 10:00 AM",
    );
  } finally {
    await memberContext.close();
  }
});
