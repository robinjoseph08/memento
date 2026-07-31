import type { Media } from "../types/generated/library";

export type Destination = "photos" | "events" | "favorites" | "search";

export type OpenedEvent = {
  id: string;
  title?: string;
  publication_id?: string;
};

export type RefreshedMediaAccess =
  "available" | "backing-unavailable" | "withdrawn" | "access-unconfirmed";

export type OpenMedia = (
  media: Media,
  refreshListingAccess: () => Promise<RefreshedMediaAccess>,
) => void;
