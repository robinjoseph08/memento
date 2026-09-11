import { use, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { useSignOut } from "../../hooks/queries/identity";
import type { useTheme } from "../../hooks/use-theme";
import { UnsavedChangesContext } from "../../lib/forms";
import { errorMessage } from "../../lib/http";
import { initials } from "../../lib/initials";
import type { Person } from "../../types/generated/identity";
import { ConnectionDetails } from "../connection/connection-status";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
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
  const unsavedRef = use(UnsavedChangesContext);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [signOutOpen, setSignOutOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  function returnFocusToTrigger(event: Event) {
    event.preventDefault();
    triggerRef.current?.focus();
  }
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
              {person.avatar_url && (
                <AvatarImage alt="" src={person.avatar_url} />
              )}
              <AvatarFallback>{initials(person.display_name)}</AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-64 max-w-[calc(100vw-1rem)]"
          onCloseAutoFocus={(event) => {
            if (connectionOpen || signOutOpen) event.preventDefault();
          }}
        >
          <DropdownMenuLabel>
            <p className="wrap-anywhere">{person.display_name}</p>
            <p className="text-xs font-normal text-muted">
              {person.is_curator ? "Curator" : "Member"}
            </p>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild>
            <Link to="/profile">Profile</Link>
          </DropdownMenuItem>
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
              if (signOut.isPending) return;
              if (unsavedRef?.current.size) {
                signOut.reset();
                setSignOutOpen(true);
                return;
              }
              event.preventDefault();
              signOut.mutate();
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
        <DialogContent onCloseAutoFocus={returnFocusToTrigger}>
          <DialogTitle className="pr-10">Immich connection</DialogTitle>
          <DialogDescription className="mt-3 text-sm text-muted">
            Check the connection to your photo library.
          </DialogDescription>
          <ConnectionDetails area="curator" />
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Sign out"
        description="Your changes will not be saved."
        error={signOut.error}
        onCloseAutoFocus={returnFocusToTrigger}
        onConfirm={() =>
          signOut.mutate(undefined, { onSuccess: () => setSignOutOpen(false) })
        }
        onOpenChange={setSignOutOpen}
        open={signOutOpen}
        pending={signOut.isPending}
        pendingLabel="Signing out…"
        title="Sign out?"
      />
    </>
  );
}
