import { Bell } from "lucide-react";
import { useId, useRef, useState } from "react";
import { Link } from "react-router-dom";

import {
  useMarkAllNotificationsRead,
  useNotifications,
} from "../../hooks/queries/notifications";
import { errorMessage } from "../../lib/http";
import { countLabel } from "../albums/moment-labels";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { NotificationRow } from "../viewer/notification-row";

// The bell holds only new updates. Everything ever sent lives on the Updates
// page, which the popover links to.
export function NotificationBell() {
  const notifications = useNotifications();
  const markAllRead = useMarkAllNotificationsRead();
  const [open, setOpen] = useState(false);
  const headingId = useId();
  const allLinkRef = useRef<HTMLAnchorElement>(null);
  const unread = notifications.data?.unread ?? 0;
  const fresh =
    notifications.data?.notifications.filter((item) => !item.read_at) ?? [];
  const label = unread
    ? `Updates, ${countLabel(unread, "unread", "unread")}`
    : "Updates";
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
        aria-labelledby={headingId}
        className="w-88 max-w-[calc(100vw-1rem)] p-2"
      >
        <p className="px-2 pt-1 pb-2 font-heading text-lg" id={headingId}>
          New updates
        </p>
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
        {notifications.data && fresh.length === 0 && (
          <p className="px-2 py-2 text-xs text-muted">
            Nothing new right now. Your Curator will let you know when there are
            new photos to see.
          </p>
        )}
        {fresh.length > 0 && (
          <ul className="max-h-[60vh] overflow-y-auto">
            {fresh.map((notification) => (
              <NotificationRow
                key={notification.id}
                notification={notification}
                onMarkedRead={() => allLinkRef.current?.focus()}
                onOpened={() => setOpen(false)}
              />
            ))}
          </ul>
        )}
        {markAllRead.isError && (
          <p className="px-2 py-2 text-xs text-destructive" role="alert">
            {errorMessage(markAllRead.error)}
          </p>
        )}
        <div className="mt-1 flex flex-wrap items-center justify-between gap-2">
          <Button
            asChild
            className="h-auto min-h-0 px-2 py-2 text-xs"
            variant="ghost"
          >
            <Link
              onClick={() => setOpen(false)}
              ref={allLinkRef}
              to="/notifications"
            >
              See all updates
            </Link>
          </Button>
          {notifications.data &&
            notifications.data.notifications.length > 0 && (
              <Button
                className="h-auto min-h-0 px-2 py-2 text-xs"
                disabled={fresh.length === 0 || markAllRead.isPending}
                onClick={() => markAllRead.mutate()}
                variant="ghost"
              >
                {fresh.length === 0
                  ? "All caught up"
                  : markAllRead.isPending
                    ? "Marking as read…"
                    : "Mark all as read"}
              </Button>
            )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
