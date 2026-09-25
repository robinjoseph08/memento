import { captureRange, mediaCounts } from "./labels";

it.each([
  [{ photo_count: 12, video_count: 1 }, "12 photos, 1 video"],
  [{ photo_count: 1, video_count: 0 }, "1 photo"],
  [{ photo_count: 0, video_count: 2 }, "2 videos"],
  [{ photo_count: 0, video_count: 0 }, ""],
])("describes %o as %s", (album, label) => {
  expect(mediaCounts(album)).toBe(label);
});

it.each([
  [
    { start_date: "2026-07-04", end_date: "2026-07-05" },
    "July 4 to July 5, 2026",
  ],
  [
    { start_date: "2025-12-31", end_date: "2026-01-01" },
    "December 31, 2025 to January 1, 2026",
  ],
  [{ start_date: "2026-07-04", end_date: "2026-07-04" }, "July 4, 2026"],
  [{ start_date: "", end_date: "2026-07-04" }, "July 4, 2026"],
  [{ start_date: "", end_date: "" }, ""],
])("spans %o as %s", (album, label) => {
  expect(captureRange(album)).toBe(label);
});
