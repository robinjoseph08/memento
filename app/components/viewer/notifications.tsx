import { BellOff, CheckCheck } from "lucide-react";
import { Navigate } from "react-router-dom";

import { useIdentityStatus } from "../../hooks/queries/identity";
import {
  useMarkAllNotificationsRead,
  useNotifications,
} from "../../hooks/queries/notifications";
import { errorMessage } from "../../lib/http";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { NotificationRow } from "./notification-row";

// Every update ever sent to this person, newest first. The bell shows only
// the new ones; this page keeps the history and the bulk action.
export function NotificationsPage() {
  const { data } = useIdentityStatus();
  const notifications = useNotifications();
  const markAllRead = useMarkAllNotificationsRead();
  const unread = notifications.data?.unread ?? 0;
  // Curators never receive viewer updates, so their query never runs; send
  // them to their own home instead of an endless loading line.
  if (data?.person?.is_curator) return <Navigate replace to="/curator" />;
  return (
    <div className="pt-4 min-[761px]:pt-11">
      <PageTitle title="Updates" />
      <div className="flex flex-wrap items-center justify-between gap-5">
        <h1 className="font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] tracking-[-1px]">
          Updates
        </h1>
        {notifications.data && notifications.data.notifications.length > 0 && (
          <Button
            disabled={unread === 0 || markAllRead.isPending}
            onClick={() => markAllRead.mutate()}
            variant="outline"
          >
            <CheckCheck
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            {unread === 0
              ? "All caught up"
              : markAllRead.isPending
                ? "Marking as read…"
                : "Mark all as read"}
          </Button>
        )}
      </div>
      {markAllRead.isError && (
        <p className="mt-4 text-sm text-destructive" role="alert">
          {errorMessage(markAllRead.error)}
        </p>
      )}
      {notifications.isPending && (
        <p className="mt-9 text-muted" role="status">
          Loading updates…
        </p>
      )}
      {notifications.isError ? (
        <section className="mt-9">
          <p role="alert">Could not load updates. Please try again.</p>
          <Button
            className="mt-3"
            disabled={notifications.isFetching}
            onClick={() => void notifications.refetch()}
            variant="outline"
          >
            Try again
          </Button>
        </section>
      ) : notifications.data?.notifications.length === 0 ? (
        <EmptyState className="mt-9" icon={BellOff} title="No updates yet">
          Your Curator will let you know here when there are new photos or
          videos to see.
        </EmptyState>
      ) : (
        notifications.data && (
          <ul className="mt-9 max-w-[640px] divide-y divide-border border-y border-border">
            {notifications.data.notifications.map((notification) => (
              <NotificationRow
                key={notification.id}
                notification={notification}
              />
            ))}
          </ul>
        )
      )}
    </div>
  );
}
