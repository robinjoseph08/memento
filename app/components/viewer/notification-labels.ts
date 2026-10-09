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

// Every Album in the "New albums you can view" section, shown or collapsed.
export function offeredCount(notification: Notification) {
  return notification.offered_albums.length + notification.more_offered_albums;
}

// Opening a one-Album notification goes to that Album, or to its preview
// when it is new to view; several Albums go to the ordinary Album list,
// where "More albums" sits below the viewer's own.
export function notificationDestination(notification: Notification) {
  const offered = offeredCount(notification);
  if (notification.albums.length === 1 && offered === 0)
    return `/albums/${encodeURIComponent(notification.albums[0].id)}/photos`;
  if (notification.albums.length === 0 && offered === 1)
    return `/albums/${encodeURIComponent(notification.offered_albums[0].id)}/preview/photos`;
  return "/albums";
}

export function notificationTitle(notification: Notification) {
  const titles = notification.albums.map((album) => album.title);
  if (titles.length === 0) {
    const offered = offeredCount(notification);
    return offered === 1
      ? notification.offered_albums[0].title
      : countLabel(
          offered,
          "new album you can view",
          "new albums you can view",
        );
  }
  return titles.length === 1
    ? titles[0]
    : `${countLabel(titles.length, "album", "albums")}: ${titles.join(", ")}`;
}

// Counts what is new the way the update email does, leaving out a kind with
// nothing in it.
function mediaLabel(photos: number, videos: number) {
  const photoLabel = countLabel(photos, "photo", "photos");
  const videoLabel = countLabel(videos, "video", "videos");
  if (photos > 0 && videos > 0) return `${photoLabel} and ${videoLabel}`;
  return videos > 0 ? videoLabel : photoLabel;
}

// What the notification brings: media in the viewer's own Albums, or else
// the Albums new to view.
export function notificationSummary(notification: Notification) {
  const albums = notification.albums;
  if (albums.length > 0) {
    const photos = albums.reduce((sum, album) => sum + album.photo_count, 0);
    const videos = albums.reduce((sum, album) => sum + album.video_count, 0);
    const media = mediaLabel(photos, videos);
    if (albums.length !== 1) return media;
    return `${media} · ${albums[0].status === "new" ? "New album" : "Updated"}`;
  }
  return offeredCount(notification) === 1
    ? "New album you can view"
    : offeredTitles(notification);
}

// The extra line for Albums new to view beside own Album changes, or empty.
export function offeredLine(notification: Notification) {
  if (notification.albums.length === 0 || offeredCount(notification) === 0)
    return "";
  return `New albums you can view: ${offeredTitles(notification)}`;
}

// The newest few Albums new to view, then how many more there were.
function offeredTitles(notification: Notification) {
  const titles = notification.offered_albums
    .map((album) => album.title)
    .join(", ");
  return notification.more_offered_albums > 0
    ? `${titles} and ${countLabel(notification.more_offered_albums, "more album", "more albums")}`
    : titles;
}
