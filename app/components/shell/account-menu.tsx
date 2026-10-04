import {
  CircleUser,
  Crown,
  Eye,
  LogOut,
  Moon,
  Settings,
  type LucideIcon,
} from "lucide-react";
import { use, useRef, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";

import { useSignOut } from "../../hooks/queries/identity";
import type { useTheme } from "../../hooks/use-theme";
import { UnsavedChangesContext } from "../../lib/forms";
import { errorMessage } from "../../lib/http";
import type { Person } from "../../types/generated/identity";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { inViewerArea } from "./navigation";

export function AccountMenu({
  person,
  theme,
  setTheme,
  preview = false,
  onboarded = true,
}: {
  person: Person;
  preview?: boolean;
  onboarded?: boolean;
} & ReturnType<typeof useTheme>) {
  const signOut = useSignOut();
  const viewing = inViewerArea(useLocation().pathname);
  const unsavedRef = use(UnsavedChangesContext);
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
              <AvatarFallback name={person.display_name} />
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-64 max-w-[calc(100vw-1rem)]"
          onCloseAutoFocus={(event) => {
            if (signOutOpen) event.preventDefault();
          }}
        >
          <DropdownMenuLabel>
            <p className="wrap-anywhere">{person.display_name}</p>
            <p className="flex items-center gap-1 text-xs font-normal text-muted">
              {person.is_curator && (
                <Crown
                  aria-hidden="true"
                  className="size-3 text-accent-foreground"
                  strokeWidth={1.5}
                />
              )}
              {person.is_curator ? "Curator" : "Member"}
            </p>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <MenuLink
            disabled={preview || !onboarded}
            icon={CircleUser}
            to="/profile"
          >
            Profile
          </MenuLink>
          <DropdownMenuCheckboxItem
            checked={theme === "dark"}
            onCheckedChange={(checked) => setTheme(checked ? "dark" : "light")}
            onSelect={(event) => event.preventDefault()}
          >
            <MenuIcon icon={Moon} />
            Dark mode
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          {person.is_curator && onboarded && (
            <>
              <MenuLink
                disabled={preview}
                icon={viewing ? Crown : Eye}
                to={viewing ? "/curator" : "/albums"}
              >
                {viewing ? "Switch to curating" : "Switch to viewing"}
              </MenuLink>
              <MenuLink
                disabled={preview}
                icon={Settings}
                to="/curator/settings"
              >
                Settings
              </MenuLink>
            </>
          )}
          <DropdownMenuItem
            disabled={preview || signOut.isPending}
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
            <MenuIcon icon={LogOut} />
            {signOut.isPending ? "Signing out…" : "Sign out"}
          </DropdownMenuItem>
          {signOut.isError && (
            <p className="px-3 py-2 text-sm text-destructive" role="alert">
              {errorMessage(signOut.error)}
            </p>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
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

// MenuLink is a menu item that navigates, shown disabled while the account
// actions are unavailable.
function MenuLink({
  to,
  icon,
  disabled,
  children,
}: {
  to: string;
  icon: LucideIcon;
  disabled: boolean;
  children: ReactNode;
}) {
  if (disabled)
    return (
      <DropdownMenuItem disabled>
        <MenuIcon icon={icon} />
        {children}
      </DropdownMenuItem>
    );
  return (
    <DropdownMenuItem asChild>
      <Link to={to}>
        <MenuIcon icon={icon} />
        {children}
      </Link>
    </DropdownMenuItem>
  );
}

function MenuIcon({ icon: Icon }: { icon: LucideIcon }) {
  return (
    <Icon aria-hidden="true" className="size-4 text-muted" strokeWidth={1.5} />
  );
}
