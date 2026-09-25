// mediaCounts describes what an Album holds, as "12 photos, 1 video", for
// screen readers; the gallery shows the same counts beside icons.
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

// captureDate formats a capture day as "Jul 4, 2026". The month is short
// because a gallery tile is narrow. The day is taken as written, never
// shifted into the phone's time zone.
export function captureDate(value: string) {
  if (!value) {
    return "";
  }
  const date = new Date(`${value.slice(0, 10)}T12:00:00Z`);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(date);
}

// captureRange is the span of an Album's capture days, as "Jul 4 to Jul 5,
// 2026" within one year and with both years otherwise.
export function captureRange({
  start_date,
  end_date,
}: {
  start_date: string;
  end_date: string;
}) {
  const start = captureDate(start_date);
  const end = captureDate(end_date);
  if (!start || !end || start === end) {
    return start || end;
  }
  const sameYear = start_date.slice(0, 4) === end_date.slice(0, 4);
  return `${sameYear ? start.replace(/, \d{4}$/, "") : start} to ${end}`;
}
