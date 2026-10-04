import {
  Blend,
  House,
  Images,
  Inbox,
  LayoutGrid,
  Send,
  Users,
  type LucideIcon,
} from "lucide-react";

import type { Person } from "../../types/generated/identity";

export type NavigationItem = {
  to: string;
  label: string;
  icon: LucideIcon;
  // Only an exact match counts as the current page.
  end?: boolean;
  // Shows the pending Access Request count beside the label.
  pending?: boolean;
};

// A Curator browsing the viewer pages gets the viewer navigation, so Albums
// and Library behave the way they do for everyone else.
export function inViewerArea(pathname: string) {
  return /^\/(albums|library)(\/|$)/.test(pathname);
}

// Where the header has room for the full navigation; below it the phone
// sheet takes over. The Curator navigation has more destinations, so it
// needs more width. Tailwind only sees whole class names, so each is spelled
// out.
export function navigationFit(person: Person, pathname: string) {
  return person.is_curator && !inViewerArea(pathname)
    ? {
        query: "(min-width: 1025px)",
        show: "min-[1025px]:flex",
        hide: "min-[1025px]:hidden",
      }
    : {
        query: "(min-width: 601px)",
        show: "min-[601px]:flex",
        hide: "min-[601px]:hidden",
      };
}

// The main navigation for a signed-in Person. The desktop header and the
// phone sheet render the same list.
export function navigationItems(
  person: Person,
  pathname: string,
): NavigationItem[] {
  if (person.is_curator && !inViewerArea(pathname))
    return [
      { to: "/curator", label: "Home", icon: House, end: true },
      { to: "/curator/albums", label: "Albums", icon: Images, end: true },
      { to: "/curator/people", label: "People", icon: Users },
      { to: "/curator/circles", label: "Circles", icon: Blend },
      {
        to: "/curator/requests",
        label: "Requests",
        icon: Inbox,
        pending: true,
      },
      { to: "/curator/updates", label: "Updates", icon: Send },
    ];
  return [
    { to: "/albums", label: "Albums", icon: Images, end: true },
    { to: "/library", label: "Library", icon: LayoutGrid },
  ];
}
