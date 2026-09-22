// mediaCounts describes what an Album holds, as "12 photos, 1 video".
export function mediaCounts({
  photo_count,
  video_count,
}: {
  photo_count: number;
  video_count: number;
}) {
  const parts = [];
  if (photo_count > 0) {
    parts.push(`${photo_count} ${photo_count === 1 ? "photo" : "photos"}`);
  }
  if (video_count > 0) {
    parts.push(`${video_count} ${video_count === 1 ? "video" : "videos"}`);
  }
  return parts.join(", ");
}
