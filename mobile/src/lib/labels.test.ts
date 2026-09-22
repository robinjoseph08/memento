import { mediaCounts } from "./labels";

it.each([
  [{ photo_count: 12, video_count: 1 }, "12 photos, 1 video"],
  [{ photo_count: 1, video_count: 0 }, "1 photo"],
  [{ photo_count: 0, video_count: 2 }, "2 videos"],
  [{ photo_count: 0, video_count: 0 }, ""],
])("describes %o as %s", (album, label) => {
  expect(mediaCounts(album)).toBe(label);
});
