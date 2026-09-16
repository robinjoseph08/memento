import { expect, it } from "vitest";

import type { ViewerDay } from "../../types/generated/publishing";
import { viewerRanges } from "./viewer";

const days: ViewerDay[] = [
  {
    date: "2026-07-05",
    photo_count: 300,
    video_count: 0,
    photo_ratios: [],
    video_ratios: [],
  },
  {
    date: "2026-07-04",
    photo_count: 100,
    video_count: 0,
    photo_ratios: [],
    video_ratios: [],
  },
  {
    date: "2026-07-03",
    photo_count: 0,
    video_count: 2,
    photo_ratios: [],
    video_ratios: [],
  },
  {
    date: "2026-07-02",
    photo_count: 300,
    video_count: 0,
    photo_ratios: [],
    video_ratios: [],
  },
  {
    date: "2026-07-01",
    photo_count: 300,
    video_count: 0,
    photo_ratios: [],
    video_ratios: [],
  },
];

it("keeps library days newest first inside each run with nonoverlapping ascending bounds", () => {
  expect(viewerRanges(days, "photos", true)).toEqual([
    { from: "2026-07-05", to: "", days: [days[0]] },
    { from: "2026-07-02", to: "2026-07-05", days: [days[1], days[3]] },
    { from: "", to: "2026-07-02", days: [days[4]] },
  ]);
  expect(viewerRanges(days, "videos", true)).toEqual([
    { from: "", to: "", days: [days[2]] },
  ]);
});

it("leaves small library requests unbounded and does not reverse the source data", () => {
  const small = [days[0], days[1]];
  expect(viewerRanges(small, "photos", true)).toEqual([
    { from: "", to: "", days: small },
  ]);
  expect(small).toEqual([days[0], days[1]]);
  const oldestFirst = [...small].reverse();
  expect(viewerRanges(oldestFirst, "photos")).toEqual([
    { from: "", to: "", days: oldestFirst },
  ]);
});
