import { expect, it } from "vitest";

import type { ViewerEntry } from "../../types/generated/publishing";
import { aspectRatio, monthLabel } from "./labels";

const entry = (width: number, height: number) =>
  ({ width, height }) as ViewerEntry;

it("truncates ratios to three decimals like the API and defaults to 3:2", () => {
  expect(aspectRatio(entry(4032, 3024))).toBe(1.333);
  expect(aspectRatio(entry(1172, 2082))).toBe(0.562);
  expect(aspectRatio(entry(0, 0))).toBe(1.5);
});

it("names a month the way the timeline shows it", () => {
  expect(monthLabel("2025-08")).toBe("Aug 2025");
});
