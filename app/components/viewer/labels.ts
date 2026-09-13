import type {
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";

// Width-to-height ratio for layout; media without dimensions lays out as 3:2.
export function aspectRatio(entry: ViewerEntry) {
  return entry.width > 0 && entry.height > 0 ? entry.width / entry.height : 1.5;
}

export function captureDate(value: string, weekday = false) {
  if (!value) return "";
  const date = new Date(`${value.slice(0, 10)}T12:00:00Z`);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat("en-US", {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
    ...(weekday ? ({ weekday: "long" } as const) : {}),
  }).format(date);
}

export function captureRange(album: ViewerAlbum) {
  const start = captureDate(album.start_date);
  const end = captureDate(album.end_date);
  if (!start || !end || start === end) return start || end;
  const sameYear = album.start_date.slice(0, 4) === album.end_date.slice(0, 4);
  return `${sameYear ? start.replace(/, \d{4}$/, "") : start} to ${end}`;
}
