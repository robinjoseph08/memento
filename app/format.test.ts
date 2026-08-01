import { expect, test } from "vitest";

import { formatDateOnly, formatDateRange } from "./format";

test("formats date-only presentation values without browser timezone reinterpretation", () => {
  const expected = new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeZone: "UTC",
  }).format(new Date("2026-01-01T00:00:00Z"));

  expect(formatDateOnly("2026-01-01")).toBe(expected);
  expect(formatDateRange("2026-01-01", "2026-01-01")).toBe(expected);
  expect(formatDateOnly("2026-02-30")).toBe("Unknown date");
  expect(formatDateRange(null, null)).toBe("No date range");
});
