import { useEffect, useState } from "react";
import { NavLink } from "react-router-dom";

import type { Person } from "../../types/generated/identity";
import { Button } from "../ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "../ui/sheet";
import { Wordmark } from "./wordmark";

export function MobileNavigation({ person }: { person: Person }) {
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
          <svg
            aria-hidden="true"
            fill="none"
            height="22"
            stroke="currentColor"
            strokeLinecap="round"
            strokeWidth="1.5"
            viewBox="0 0 24 24"
            width="22"
          >
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </Button>
      </SheetTrigger>
      <SheetContent
        aria-describedby={undefined}
        onCloseAutoFocus={(event) => {
          if (window.matchMedia("(min-width: 601px)").matches) {
            event.preventDefault();
            document
              .querySelector<HTMLAnchorElement>('a[aria-label="memento home"]')
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
          <NavLink
            className="cursor-pointer rounded-md px-3 py-3 text-sm hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring aria-[current=page]:bg-surface"
            end
            onClick={() => setOpen(false)}
            to={person.is_curator ? "/curator" : "/albums"}
          >
            Albums
          </NavLink>
          {person.is_curator && (
            <NavLink
              className="cursor-pointer rounded-md px-3 py-3 text-sm hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring aria-[current=page]:bg-surface"
              onClick={() => setOpen(false)}
              to="/curator/people"
            >
              People
            </NavLink>
          )}
        </nav>
      </SheetContent>
    </Sheet>
  );
}
