// PROTOTYPE. The notification bell and Updates list ticket 14 adds to the
// shell. One fictional update, read state kept in memory.
import { Bell } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";

import { useIdentityStatus } from "../../../hooks/queries/identity";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../../ui/popover";

export function NotificationBell() {
  const { data } = useIdentityStatus();
  const [read, setRead] = useState(false);
  const [open, setOpen] = useState(false);
  if (!data?.person || data.person.is_curator) return null;
  return (
    <Popover onOpenChange={setOpen} open={open}>
      <PopoverTrigger asChild>
        <Button
          aria-label={read ? "Updates" : "Updates, 1 unread"}
          className="relative size-11 rounded-full p-0"
          variant="ghost"
        >
          <Bell aria-hidden="true" className="size-5" strokeWidth={1.5} />
          {!read && (
            <span className="absolute top-2 right-2 size-2 rounded-full bg-primary" />
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-2">
        <p className="px-2 pt-1 pb-2 font-heading text-lg">Updates</p>
        <Link
          className={cn(
            "flex gap-3 rounded-md p-2 hover:bg-surface",
            read && "text-muted",
          )}
          onClick={() => {
            setRead(true);
            setOpen(false);
          }}
          to="/albums/lake"
        >
          <img
            alt=""
            className="h-12 w-16 shrink-0 rounded-sm object-cover"
            src="/prototype-media/photo-07.jpg"
          />
          <span className="min-w-0">
            <strong className="block truncate font-medium">
              A weekend by the lake
            </strong>
            <span className="block text-xs text-muted">
              113 photos and 2 videos
            </span>
            <span className="mt-1 block text-xs">
              Finally got these together. Enjoy!
            </span>
          </span>
          {!read && (
            <span
              aria-hidden="true"
              className="mt-2 size-2 shrink-0 rounded-full bg-primary"
            />
          )}
        </Link>
        <Button
          className="mt-1 h-auto min-h-0 w-full justify-start px-2 py-2 text-xs"
          disabled={read}
          onClick={() => setRead(true)}
          variant="ghost"
        >
          {read ? "All caught up" : "Mark as read"}
        </Button>
      </PopoverContent>
    </Popover>
  );
}
