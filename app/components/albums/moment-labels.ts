import type { Entry, Moment } from "../../types/generated/publishing";

export function countLabel(count: number, singular: string, plural: string) {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function countMedia(entries: Entry[]) {
  return {
    photos: entries.filter((entry) => entry.kind === "IMAGE").length,
    videos: entries.filter((entry) => entry.kind === "VIDEO").length,
  };
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

// captureClock is the 12-hour clock with AM or PM on the same line that
// every capture-time overlay and review row shows.
export function captureClock(capturedAt: string) {
  const hour = Number(capturedAt.slice(11, 13));
  const clock = `${hour % 12 || 12}:${capturedAt.slice(14, 16)}`;
  return `${clock} ${hour < 12 ? "AM" : "PM"}`;
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

// The title a viewer sees when no Memento video title is set: the filename
// without its extension.
export function filenameTitle(filename: string) {
  return filename.replace(/\.[^.]+$/, "");
}
