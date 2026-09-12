import type { Entry, Moment } from "../../types/generated/publishing";

export function countLabel(count: number, singular: string, plural: string) {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function mediaCounts(entries: Entry[]) {
  const photos = entries.filter((entry) => entry.kind === "IMAGE").length;
  const videos = entries.filter((entry) => entry.kind === "VIDEO").length;
  return videos
    ? `${countLabel(photos, "photo", "photos")}, ${countLabel(videos, "video", "videos")}`
    : countLabel(photos, "photo", "photos");
}

// Titled Moments show their date beneath the title; untitled ones use the
// weekday date as the title.
export function momentHeading(moment: Moment) {
  const date = new Date(`${moment.date}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return { title: moment.label, date: "" };
  const options = {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
  } as const;
  const original = date.toLocaleDateString("en-US", options);
  const dated = date.toLocaleDateString("en-US", {
    ...options,
    weekday: "long",
  });
  return {
    title: moment.label === original ? dated : moment.label,
    date: moment.title ? dated : "",
  };
}

export function shortDay(day: string) {
  const date = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
  });
}

export function momentCover(moment: Moment) {
  return moment.entries.find((entry) => entry.id === moment.cover_entry_id);
}
