import { useNavigate } from "react-router-dom";

import { useMarkNotificationRead } from "../../hooks/queries/notifications";
import { errorMessage } from "../../lib/http";
import { cn } from "../../lib/utils";
import type { Notification } from "../../types/generated/notifications";
import { countLabel } from "../albums/moment-labels";
import {
  notificationDate,
  notificationDestination,
  notificationTitle,
} from "../shell/notification-labels";
import { Button } from "../ui/button";

// One update, shared by the bell and the Updates page. Opening marks it read
// and goes to its Album, or to the Album list when it covers several. A
// failed mark-read keeps the person where they are and shows why.
export function NotificationRow({
  notification,
  onOpened,
}: {
  notification: Notification;
  onOpened?: () => void;
}) {
  const markRead = useMarkNotificationRead();
  const navigate = useNavigate();
  const unread = !notification.read_at;
  const photos = notification.albums.reduce(
    (sum, album) => sum + album.photo_count,
    0,
  );
  const videos = notification.albums.reduce(
    (sum, album) => sum + album.video_count,
    0,
  );
  const title = notificationTitle(notification);
  async function open() {
    if (unread) {
      try {
        await markRead.mutateAsync(notification.id);
      } catch {
        return;
      }
    }
    onOpened?.();
    await navigate(notificationDestination(notification));
  }
  return (
    <li
      className={cn(
        "flex gap-2 rounded-md p-2 hover:bg-surface",
        !unread && "text-muted",
      )}
      data-unread={unread}
    >
      <button
        aria-label={`Open ${title}`}
        className="min-w-0 flex-1 cursor-pointer rounded-sm text-left focus-visible:outline-2 focus-visible:outline-ring"
        disabled={markRead.isPending}
        onClick={() => void open()}
        type="button"
      >
        <span className="block font-medium wrap-anywhere">{title}</span>
        <span className="block text-xs text-muted">
          {countLabel(photos, "photo", "photos")} and{" "}
          {countLabel(videos, "video", "videos")}
          {notification.albums.length === 1 &&
            ` · ${notification.albums[0].status === "new" ? "New album" : "Updated"}`}
          {" · "}
          {notificationDate(notification.created_at)}
        </span>
        {notification.note && (
          <span className="mt-1 block text-xs wrap-anywhere">
            {notification.note}
          </span>
        )}
        {markRead.isError && (
          <span className="mt-1 block text-xs text-destructive" role="alert">
            {errorMessage(markRead.error)}
          </span>
        )}
      </button>
      {unread && (
        <Button
          aria-label={`Mark ${title} as read`}
          className="mt-0.5 h-auto min-h-0 shrink-0 px-2 py-1 text-xs"
          disabled={markRead.isPending}
          onClick={() => markRead.mutate(notification.id)}
          size="sm"
          variant="ghost"
        >
          <span aria-hidden="true" className="size-2 rounded-full bg-primary" />
          <span className="sr-only">Mark as read</span>
        </Button>
      )}
    </li>
  );
}
