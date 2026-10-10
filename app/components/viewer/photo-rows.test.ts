import { expect, it } from "vitest";

import { photoRows } from "./photo-rows";

const layout = (ratios: number[], target: number) =>
  photoRows(ratios, target).map(({ items, share }) => ({ items, share }));

it("lets a wide photo overfill a row rather than leave it short", () => {
  expect(layout([1.5, 1.5, 1.778, 1.5, 1.5], 4.5)).toEqual([
    { items: [0, 1, 2], share: 1 },
    { items: [3, 4], share: 3 / 4.5 },
  ]);
});

it("starts a new row when a photo would carry it further past the target", () => {
  expect(layout([1.5, 1.5, 1.5, 1.5, 1.5, 1.5], 4.5)).toEqual([
    { items: [0, 1, 2], share: 1 },
    { items: [3, 4, 5], share: 1 },
  ]);
});

it("pairs a portrait with a landscape photo on a phone", () => {
  expect(layout([0.667, 1.5, 1.5], 1.5)).toEqual([
    { items: [0, 1], share: 1 },
    { items: [2], share: 1 },
  ]);
});

it("keeps a lone portrait before a panorama at its natural size", () => {
  expect(layout([0.667, 8, 1.5], 4.5)).toEqual([
    { items: [0], share: 0.667 / 4.5 },
    { items: [1], share: 1 },
    { items: [2], share: 1.5 / 4.5 },
  ]);
});
