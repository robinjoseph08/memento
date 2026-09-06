import { useRef, useState } from "react";

import { useSignOut } from "../../hooks/queries/identity";
import type { useTheme } from "../../hooks/use-theme";
import { errorMessage } from "../../lib/http";
import type { Person } from "../../types/generated/identity";
import { ConnectionDetails } from "../connection/connection-status";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

export function AccountMenu({
  person,
  theme,
  setTheme,
}: { person: Person } & ReturnType<typeof useTheme>) {
  const signOut = useSignOut();
  const [connectionOpen, setConnectionOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const initials = person.display_name
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((name) => Array.from(name)[0])
    .join("")
    .toLocaleUpperCase();
  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            aria-label="Account menu"
            className="size-11 rounded-full border border-border bg-surface p-0"
            ref={triggerRef}
            variant="ghost"
          >
            <Avatar aria-hidden="true" className="size-full">
              <AvatarFallback>
                <span className="avatar-initials">{initials}</span>
              </AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-64 max-w-[calc(100vw-1rem)]"
          onCloseAutoFocus={(event) => {
            if (connectionOpen) event.preventDefault();
          }}
        >
          <DropdownMenuLabel>
            <p className="wrap-anywhere">{person.display_name}</p>
            {person.is_curator && (
              <p className="text-xs font-normal text-muted">Curator</p>
            )}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuCheckboxItem
            checked={theme === "dark"}
            onCheckedChange={(checked) => setTheme(checked ? "dark" : "light")}
            onSelect={(event) => event.preventDefault()}
          >
            Dark mode
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          {person.is_curator && (
            <DropdownMenuItem onSelect={() => setConnectionOpen(true)}>
              Immich connection
            </DropdownMenuItem>
          )}
          <DropdownMenuItem
            disabled={signOut.isPending}
            onSelect={(event) => {
              event.preventDefault();
              if (!signOut.isPending) signOut.mutate();
            }}
          >
            {signOut.isPending ? "Signing out…" : "Sign out"}
          </DropdownMenuItem>
          {signOut.isError && (
            <p className="px-3 py-2 text-sm text-destructive" role="alert">
              {errorMessage(signOut.error)}
            </p>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <Dialog onOpenChange={setConnectionOpen} open={connectionOpen}>
        <DialogContent
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
        >
          <DialogTitle className="pr-10">Immich connection</DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            Check the connection to your photo library.
          </DialogDescription>
          <ConnectionDetails area="curator" />
        </DialogContent>
      </Dialog>
    </>
  );
}
