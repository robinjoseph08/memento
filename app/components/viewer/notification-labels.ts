import type { Notification } from "../../types/generated/notifications";
import { countLabel } from "../albums/moment-labels";

// The viewer never shows clock times, so a notification carries its date only.
export function notificationDate(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("en-US", {
    month: "long",
    day: "numeric",
    year: "numeric",
  });
}

// Opening a one-Album notification goes to that Album; several Albums go to
// the ordinary Album list rather than a list of links.
export function notificationDestination(notification: Notification) {
  return notification.albums.length === 1
    ? `/albums/${encodeURIComponent(notification.albums[0].id)}/photos`
    : "/albums";
}

export function notificationTitle(notification: Notification) {
  const titles = notification.albums.map((album) => album.title);
  return titles.length === 1
    ? titles[0]
    : `${countLabel(titles.length, "album", "albums")}: ${titles.join(", ")}`;
}
