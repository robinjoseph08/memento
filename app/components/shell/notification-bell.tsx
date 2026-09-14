import { Bell } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";

import {
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotifications,
} from "../../hooks/queries/notifications";
import { errorMessage } from "../../lib/http";
import { cn } from "../../lib/utils";
import type { Notification } from "../../types/generated/notifications";
import { countLabel } from "../albums/moment-labels";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import {
  notificationDate,
  notificationDestination,
  notificationTitle,
} from "./notification-labels";

export function NotificationBell() {
  const notifications = useNotifications();
  const markRead = useMarkNotificationRead();
  const markAllRead = useMarkAllNotificationsRead();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const unread = notifications.data?.unread ?? 0;
  const label = unread
    ? `Updates, ${countLabel(unread, "unread", "unread")}`
    : "Updates";
  const error = markRead.error ?? markAllRead.error;
  async function openNotification(notification: Notification) {
    if (!notification.read_at) await markRead.mutateAsync(notification.id);
    setOpen(false);
    await navigate(notificationDestination(notification));
  }
  return (
    <Popover onOpenChange={setOpen} open={open}>
      <PopoverTrigger asChild>
        <Button
          aria-label={label}
          className="relative size-11 rounded-full p-0"
          variant="ghost"
        >
          <Bell aria-hidden="true" className="size-5" strokeWidth={1.5} />
          {unread > 0 && (
            <span
              aria-hidden="true"
              className="absolute top-1 right-1 min-w-4 rounded-full bg-primary px-1 text-[11px]/4 font-medium text-primary-foreground"
            >
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        aria-label="Updates"
        className="w-88 max-w-[calc(100vw-1rem)] p-2"
      >
        <p className="px-2 pt-1 pb-2 font-heading text-lg">Updates</p>
        {notifications.isPending && (
          <p className="px-2 py-2 text-xs text-muted" role="status">
            Loading updates…
          </p>
        )}
        {notifications.isError && (
          <p className="px-2 py-2 text-xs text-destructive" role="alert">
            {errorMessage(notifications.error)}
          </p>
        )}
        {notifications.data?.notifications.length === 0 && (
          <p className="px-2 py-2 text-xs text-muted">
            No updates yet. Your Curator will let you know when there are new
            photos to see.
          </p>
        )}
        {notifications.data && notifications.data.notifications.length > 0 && (
          <ul className="max-h-[60vh] overflow-y-auto">
            {notifications.data.notifications.map((notification) => (
              <NotificationRow
                key={notification.id}
                notification={notification}
                onMarkRead={() => markRead.mutate(notification.id)}
                onOpen={() => void openNotification(notification)}
                pending={markRead.isPending}
              />
            ))}
          </ul>
        )}
        {error && (
          <p className="px-2 py-2 text-xs text-destructive" role="alert">
            {errorMessage(error)}
          </p>
        )}
        {notifications.data && notifications.data.notifications.length > 0 && (
          <Button
            className="mt-1 h-auto min-h-0 w-full justify-start px-2 py-2 text-xs"
            disabled={unread === 0 || markAllRead.isPending}
            onClick={() => markAllRead.mutate()}
            variant="ghost"
          >
            {unread === 0
              ? "All caught up"
              : markAllRead.isPending
                ? "Marking as read…"
                : "Mark all as read"}
          </Button>
        )}
      </PopoverContent>
    </Popover>
  );
}

function NotificationRow({
  notification,
  onOpen,
  onMarkRead,
  pending,
}: {
  notification: Notification;
  onOpen: () => void;
  onMarkRead: () => void;
  pending: boolean;
}) {
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
        disabled={pending}
        onClick={onOpen}
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
        {notification.albums.some((album) => album.video_titles.length > 0) && (
          <span className="mt-1 block text-xs wrap-anywhere text-muted">
            Videos:{" "}
            {notification.albums
              .flatMap((album) => album.video_titles)
              .join(", ")}
          </span>
        )}
        {notification.note && (
          <span className="mt-1 block text-xs wrap-anywhere">
            {notification.note}
          </span>
        )}
      </button>
      {unread && (
        <Button
          aria-label={`Mark ${title} as read`}
          className="mt-0.5 h-auto min-h-0 shrink-0 px-2 py-1 text-xs"
          disabled={pending}
          onClick={onMarkRead}
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
