import type {
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";

// Width-to-height ratio for layout, truncated to three decimals exactly as
// the API truncates a day's photo_ratios, so placeholder rows and real rows
// break at the same places. Media without dimensions lays out as 3:2.
export function aspectRatio(entry: ViewerEntry) {
  return entry.width > 0 && entry.height > 0
    ? Math.trunc((entry.width / entry.height) * 1000) / 1000
    : 1.5;
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

// monthLabel formats a YYYY-MM key the way the timeline shows it: "Aug 2025".
export function monthLabel(key: string) {
  return new Intl.DateTimeFormat("en-US", {
    timeZone: "UTC",
    month: "short",
    year: "numeric",
  }).format(new Date(`${key}-01T12:00:00Z`));
}

// dayLabel and shortDayLabel format a YYYY-MM-DD key for the timeline when it
// runs by day: "Jun 2, 2026" beside the pointer, "Jun 2" on the rail.
export function dayLabel(key: string) {
  return new Intl.DateTimeFormat("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(`${key}T12:00:00Z`));
}

export function shortDayLabel(key: string) {
  return new Intl.DateTimeFormat("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
  }).format(new Date(`${key}T12:00:00Z`));
}

export function captureRange(album: ViewerAlbum) {
  const start = captureDate(album.start_date);
  const end = captureDate(album.end_date);
  if (!start || !end || start === end) return start || end;
  const sameYear = album.start_date.slice(0, 4) === album.end_date.slice(0, 4);
  return `${sameYear ? start.replace(/, \d{4}$/, "") : start} to ${end}`;
}

// clock formats seconds as m:ss, or h:mm:ss past an hour, like player controls.
export function clock(seconds: number) {
  const whole = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const rest = whole % 60;
  const pad = (value: number) => String(value).padStart(2, "0");
  return hours > 0
    ? `${hours}:${pad(minutes)}:${pad(rest)}`
    : `${minutes}:${pad(rest)}`;
}
