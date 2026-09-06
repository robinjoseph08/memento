import { useRef, useState } from "react";

import { useSignOut } from "../../hooks/queries/identity";
import type { useTheme } from "../../hooks/use-theme";
import { errorMessage } from "../../lib/http";
import type { Person } from "../../types/generated/identity";
import { ConnectionDetails } from "../connection/connection-status";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
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
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            aria-label="Account menu"
            className="size-11 rounded-full border border-border bg-surface p-0"
            ref={triggerRef}
            variant="ghost"
          >
            <span aria-hidden="true">{initials}</span>
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
          <DropdownMenuLabel className="text-xs font-normal text-muted">
            Theme
          </DropdownMenuLabel>
          <DropdownMenuRadioGroup
            aria-label="Theme"
            onValueChange={(value) => {
              if (value === "light" || value === "dark") setTheme(value);
            }}
            value={theme}
          >
            <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
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
