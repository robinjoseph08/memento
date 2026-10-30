import { mkdir } from "node:fs/promises";
import type { Browser, Locator, Page } from "@playwright/test";

import type { Circle, Person } from "../app/types/generated/identity";
import type {
  AlbumDetail,
  PublicationReview,
  ViewerPage,
} from "../app/types/generated/publishing";
import { expect, finishOnboarding, test } from "./fixtures";

// publish imports an Immich album, makes it available as given, and
// publishes it.
async function publish(
  page: Page,
  source: string,
  access: { people?: string[]; circles?: string[] },
) {
  const headers = { Origin: new URL(page.url()).origin };
  const imported = await page.request.post("/api/curator/imports", {
    headers,
    data: { source_id: source },
  });
  expect(imported.ok()).toBe(true);
  const album = (await imported.json()) as AlbumDetail;
  const url = `/api/curator/albums/${album.id}`;
  await expect
    .poll(
      async () =>
        ((await (await page.request.get(url)).json()) as AlbumDetail).status,
      { timeout: 90_000 },
    )
    .toBe("complete");
  const saved = await page.request.post(`${url}/access`, {
    headers,
    data: {
      people: (access.people ?? []).map((id) => ({
        person_id: id,
        allowed: true,
      })),
      circles: (access.circles ?? []).map((id) => ({
        circle_id: id,
        offered: true,
      })),
    },
  });
  expect(saved.ok()).toBe(true);
  const review = (await (
    await page.request.get(`${url}/publication`)
  ).json()) as PublicationReview;
  const published = await page.request.post(`${url}/publish`, {
    headers,
    data: { review_token: review.review_token },
  });
  expect(published.ok()).toBe(true);
  return album;
}

// lit says whether points a person might tap on target, at its middle and
// near both ends, reach it, which fails when the Tip's dimming has drifted
// over any of it.
async function lit(target: Locator) {
  return target.evaluate((element) => {
    const box = element.getBoundingClientRect();
    const y = box.top + box.height / 2;
    return [box.left + 4, box.left + box.width / 2, box.right - 4].every(
      (x) => {
        const hit = document.elementFromPoint(x, y);
        return !!hit && element.contains(hit);
      },
    );
  });
}

// press taps on a touch screen and clicks with a mouse, at the target's
// position on screen, as a person would.
async function press(page: Page, target: Locator, touch: boolean) {
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  if (!box) throw new Error("target is not on screen");
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  if (touch) await page.touchscreen.tap(x, y);
  else await page.mouse.click(x, y);
}

async function capture(page: Page, name: string) {
  if (process.env.QA_CAPTURES !== "1") return;
  await mkdir("tmp/qa-captures", { recursive: true });
  await page.screenshot({
    path: `tmp/qa-captures/${test.info().project.name}-${name}.png`,
  });
}

// Each viewer meets the Tips on a different screen: a desktop with a mouse,
// a phone held upright, and a phone held sideways, where the callout has the
// least room.
const screens = [
  { name: "Desk", form: "desktop", touch: false, width: 1280, height: 800 },
  { name: "Phone", form: "phone", touch: true, width: 390, height: 844 },
  { name: "Sideways", form: "landscape", touch: true, width: 844, height: 390 },
] as const;

async function signIn(
  browser: Browser,
  baseURL: string | undefined,
  email: string,
  screen: (typeof screens)[number],
) {
  const { touch, width, height } = screen;
  const context = await browser.newContext({
    baseURL,
    hasTouch: touch,
    viewport: { width, height },
  });
  const page = await context.newPage();
  await page.goto("/sign-in");
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(email);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await finishOnboarding(page, {
    tap: touch,
    keepTips: ["videos", "join", "chapters"],
  });
  return { context, page };
}

test("viewers on a desktop and a phone meet each Tip once, pointing at the thing it names", async ({
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
  const headers = { Origin: new URL(page.url()).origin };

  const viewers: {
    person: Person;
    email: string;
    screen: (typeof screens)[number];
  }[] = [];
  for (const screen of screens) {
    const { name } = screen;
    const email = `${name.toLowerCase()}@example.test`;
    const created = await page.request.post("/api/people", {
      headers,
      data: { display_name: name },
    });
    expect(created.ok()).toBe(true);
    const person = (await created.json()) as Person;
    const approved = await page.request.post(
      `/api/people/${person.id}/preauthorizations`,
      { headers, data: { email } },
    );
    expect(approved.ok()).toBe(true);
    viewers.push({ person, email, screen });
  }
  const ids = viewers.map((viewer) => viewer.person.id);
  const circle = (await (
    await page.request.post("/api/circles", {
      headers,
      data: { name: "Everyone" },
    })
  ).json()) as Circle;
  expect(
    (
      await page.request.post(`/api/circles/${circle.id}/members`, {
        headers,
        data: { person_ids: ids },
      })
    ).ok(),
  ).toBe(true);

  // Coast has a cover above its tabs, which loads after the Tip appears.
  const coast = await publish(page, "fixture-album-coast", { people: ids });
  const videos = await publish(page, "workbench-videos", { people: ids });
  await publish(page, "fixture-album-family", { circles: [circle.id] });
  await expect
    .poll(
      async () => {
        const album = (await (
          await page.request.get(`/api/curator/albums/${videos.id}`)
        ).json()) as AlbumDetail;
        return album.moments
          .flatMap((moment) => moment.entries)
          .find((entry) => entry.filename === "birthday-party.webm")
          ?.chapter_status;
      },
      { timeout: 120_000, intervals: [1000] },
    )
    .toBe("complete");

  for (const viewer of viewers) {
    const { context, page: member } = await signIn(
      browser,
      baseURL,
      viewer.email,
      viewer.screen,
    );
    const { form, touch } = viewer.screen;
    try {
      // Join: the More albums heading is lit, and tapping the dimmed page
      // dismisses the Tip without opening what was under it.
      const join = member.getByRole("dialog", { name: "Albums you can join" });
      await expect(join).toBeVisible();
      const more = member.getByRole("heading", { name: "More albums" });
      await expect(more).toBeInViewport();
      await expect.poll(() => lit(more)).toBe(true);
      await expect(join).toHaveCSS("opacity", "1");
      await expect(join).toBeInViewport({ ratio: 1 });
      await capture(member, `join-tip-${form}`);
      const viewport = member.viewportSize()!;
      if (touch) await member.touchscreen.tap(10, viewport.height - 10);
      else await member.mouse.click(10, viewport.height - 10);
      await expect(join).toHaveCount(0);
      await expect(member).toHaveURL(/\/albums$/);

      // Videos: a photo opened by link and closed again leaves the Tip
      // waiting rather than retired.
      const viewerPhotos = (await (
        await member.request.get(`/api/albums/${coast.id}/photos`)
      ).json()) as ViewerPage;
      await member.goto(
        `/albums/${coast.id}/photos/${viewerPhotos.entries[0].id}`,
      );
      const lightbox = member.getByRole("dialog", { name: /^Photo / });
      await expect(lightbox).toBeVisible();
      const videosTip = member.getByRole("dialog", {
        name: "Videos have their own tab",
      });
      await expect(videosTip).toHaveCount(0);
      await member.keyboard.press("Escape");
      await expect(lightbox).toHaveCount(0);
      await expect(videosTip).toBeVisible();

      // The tab stays lit after the cover loads, and tapping it opens Videos.
      const tab = member
        .getByRole("navigation", { name: "Album media" })
        .getByRole("link", { name: /^Videos/ });
      await expect(
        member.getByRole("img", { name: "Album cover" }),
      ).toBeVisible();
      await expect.poll(() => lit(tab)).toBe(true);
      await expect(videosTip).toHaveCSS("opacity", "1");
      await expect(videosTip).toBeInViewport({ ratio: 1 });
      await capture(member, `videos-tip-${form}`);

      // Scrolling away with the Tip up stays put, even as the photos that
      // scroll into view load and the page changes around the target.
      if (!touch) {
        const before = await member.evaluate(() => window.scrollY);
        await member.mouse.wheel(0, 800);
        await expect
          .poll(() => member.evaluate(() => window.scrollY))
          .toBeGreaterThan(before + 300);
        await expect(tab).not.toBeInViewport();
        const scrolled = await member.evaluate(() => window.scrollY);
        await expect
          .poll(() =>
            member.evaluate(() =>
              [...document.images]
                .filter((image) => {
                  const box = image.getBoundingClientRect();
                  return box.bottom > 0 && box.top < window.innerHeight;
                })
                .every((image) => image.complete),
            ),
          )
          .toBe(true);
        // Something else on the page changes too, as a refresh would.
        await member.evaluate(() =>
          document.body.append(document.createElement("div")),
        );
        await expect
          .poll(() => member.evaluate(() => window.scrollY))
          .toBe(scrolled);
        await expect(videosTip).toBeVisible();
        await member.mouse.wheel(0, -2000);
        await expect.poll(() => lit(tab)).toBe(true);
      }
      await press(member, tab, touch);
      await expect(member).toHaveURL(/\/videos$/);
      await expect(videosTip).toHaveCount(0);

      // Chapters: opening the video from its tile shows the Tip and holds the
      // video until Got it.
      await member.goto(`/albums/${videos.id}/videos`);
      const tile = member.getByRole("link", {
        name: "Open video birthday-party",
      });
      await press(member, tile, touch);
      const chapters = member.getByRole("dialog", {
        name: "This video has chapters",
      });
      await expect(chapters).toBeVisible();
      const picker = member.getByRole("combobox", { name: "Chapter" });
      await expect(picker).toBeInViewport();
      await expect.poll(() => lit(picker)).toBe(true);
      const video = member.locator("video");
      expect(
        await video.evaluate((element: HTMLVideoElement) => element.paused),
      ).toBe(true);
      await expect(chapters).toHaveCSS("opacity", "1");
      await expect(chapters).toBeInViewport({ ratio: 1 });
      await capture(member, `chapters-tip-${form}`);
      await press(
        member,
        chapters.getByRole("button", { name: "Got it" }),
        touch,
      );
      await expect(chapters).toHaveCount(0);
      await expect(
        member.getByRole("dialog", { name: /^Video / }),
      ).toBeVisible();
      await expect
        .poll(() =>
          video.evaluate((element: HTMLVideoElement) => element.autoplay),
        )
        .toBe(true);

      // None of them come back.
      await member.reload();
      await expect(
        member.getByRole("dialog", { name: /^Video / }),
      ).toBeVisible();
      await expect(chapters).toHaveCount(0);
      await member.goto(`/albums/${coast.id}/photos`);
      await expect(
        member.getByRole("link", { name: /^Open photo/ }).first(),
      ).toBeVisible();
      await expect(videosTip).toHaveCount(0);
      await member.goto("/albums");
      await expect(more).toBeVisible();
      await expect(join).toHaveCount(0);
    } finally {
      await context.close();
    }
  }
});
