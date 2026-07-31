import { APIError } from "../api";
import type { Media } from "../types/generated/library";

function parsedMediaDate(media: Media) {
  if (!media.local_date_time) return undefined;
  const parsed = new Date(media.local_date_time);
  return Number.isNaN(parsed.valueOf()) ? undefined : parsed;
}

export function mediaMonthLabel(media: Media) {
  const parsed = parsedMediaDate(media);
  if (!parsed) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, {
    month: "long",
    year: "numeric",
  }).format(parsed);
}

export function mediaDateLabel(media: Media) {
  const parsed = parsedMediaDate(media);
  if (!parsed) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(
    parsed,
  );
}

export function mediaAlt(item: Media, index: number) {
  const kind = item.media_type === "video" ? "Video" : "Photo";
  const date = mediaMonthLabel(item);
  return date === "Date unavailable"
    ? `${kind} ${index + 1}, date unavailable`
    : `${kind} ${index + 1} from ${date}`;
}

export function classifyRefreshedMedia(current: Media | undefined) {
  if (!current) return "withdrawn" as const;
  return current.available
    ? ("available" as const)
    : ("backing-unavailable" as const);
}

export function isUnavailableResponse(error: unknown) {
  return error instanceof APIError && error.status === 404;
}
