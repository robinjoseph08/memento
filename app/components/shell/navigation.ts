import {
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

// The main navigation for a signed-in Person. The desktop header and the
// phone sheet render the same list.
export function navigationItems(person: Person): NavigationItem[] {
  if (person.is_curator)
    return [
      { to: "/curator", label: "Home", icon: House, end: true },
      { to: "/curator/albums", label: "Albums", icon: Images, end: true },
      { to: "/curator/people", label: "People", icon: Users },
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
