import { APIError } from "../api";
import type { RecipientMedia } from "./types";

function parsedMediaDate(media: RecipientMedia) {
  if (!media.local_date_time) return undefined;
  const parsed = new Date(media.local_date_time);
  return Number.isNaN(parsed.valueOf()) ? undefined : parsed;
}

function mediaCaptureDate(media: RecipientMedia) {
  return "capture_date" in media ? media.capture_date : undefined;
}

export function captureDateLabel(captureDate: string | null) {
  if (!captureDate) return "Date unavailable";
  const parsed = new Date(`${captureDate}T00:00:00Z`);
  if (Number.isNaN(parsed.valueOf())) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "long",
    timeZone: "UTC",
  }).format(parsed);
}

export function mediaMonthLabel(media: RecipientMedia) {
  const captureDate = mediaCaptureDate(media);
  if (captureDate) {
    const parsed = new Date(`${captureDate}T00:00:00Z`);
    if (Number.isNaN(parsed.valueOf())) return "Date unavailable";
    return new Intl.DateTimeFormat(undefined, {
      month: "long",
      year: "numeric",
      timeZone: "UTC",
    }).format(parsed);
  }
  const parsed = parsedMediaDate(media);
  if (!parsed) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, {
    month: "long",
    year: "numeric",
  }).format(parsed);
}

export function mediaDateLabel(media: RecipientMedia) {
  const captureDate = mediaCaptureDate(media);
  if (captureDate) return captureDateLabel(captureDate);
  const parsed = parsedMediaDate(media);
  if (!parsed) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(
    parsed,
  );
}

export function mediaAlt(item: RecipientMedia, index: number) {
  const kind = item.media_type === "video" ? "Video" : "Photo";
  const date = mediaMonthLabel(item);
  return date === "Date unavailable"
    ? `${kind} ${index + 1}, date unavailable`
    : `${kind} ${index + 1} from ${date}`;
}

export function classifyRefreshedMedia(current: RecipientMedia | undefined) {
  if (!current) return "withdrawn" as const;
  return current.available
    ? ("available" as const)
    : ("backing-unavailable" as const);
}

export function isUnavailableResponse(error: unknown) {
  return error instanceof APIError && error.status === 404;
}
