import { Menu } from "lucide-react";
import { useEffect, useState } from "react";
import { NavLink } from "react-router-dom";

import type { Person } from "../../types/generated/identity";
import { Button } from "../ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "../ui/sheet";
import { navigationItems } from "./navigation";
import { PendingBadge } from "./pending-badge";
import { Wordmark } from "./wordmark";

export function MobileNavigation({
  person,
  pendingRequests = 0,
}: {
  person: Person;
  pendingRequests?: number;
}) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 601px)");
    const closeOnDesktop = () => {
      if (desktop.matches) setOpen(false);
    };
    desktop.addEventListener("change", closeOnDesktop);
    return () => desktop.removeEventListener("change", closeOnDesktop);
  }, []);
  return (
    <Sheet onOpenChange={setOpen} open={open}>
      <SheetTrigger asChild>
        <Button
          aria-label="Open navigation"
          className="size-11 p-0 min-[601px]:hidden"
          variant="ghost"
        >
          <Menu aria-hidden="true" size={22} strokeWidth={1.5} />
        </Button>
      </SheetTrigger>
      <SheetContent
        aria-describedby={undefined}
        onCloseAutoFocus={(event) => {
          if (window.matchMedia("(min-width: 601px)").matches) {
            event.preventDefault();
            document
              .querySelector<HTMLAnchorElement>('a[aria-label="Memento home"]')
              ?.focus();
          }
        }}
      >
        <SheetTitle aria-label="Navigation" className="pt-1 pr-10">
          <Wordmark />
        </SheetTitle>
        <nav
          aria-label="Mobile navigation"
          className="mt-5 flex flex-col gap-1"
        >
          {navigationItems(person).map((item) => (
            <NavLink
              className="group inline-flex cursor-pointer items-center gap-3 rounded-md px-3 py-3 text-sm hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring aria-[current=page]:bg-surface"
              end={item.end}
              key={item.to}
              onClick={() => setOpen(false)}
              to={item.to}
            >
              <item.icon
                aria-hidden="true"
                className="size-5 text-muted group-aria-[current=page]:text-accent-foreground"
                strokeWidth={1.5}
              />
              {item.label}
              {item.pending && <PendingBadge count={pendingRequests} />}
            </NavLink>
          ))}
        </nav>
      </SheetContent>
    </Sheet>
  );
}
