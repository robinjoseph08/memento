import type { Media as LibraryMedia } from "../types/generated/library";
import type { Media as SearchMedia } from "../types/generated/search";

export type Destination = "photos" | "events" | "favorites" | "search";

export type OpenedEvent = {
  id: string;
  title?: string;
  publication_id?: string;
};

export type RefreshedMediaAccess =
  "available" | "backing-unavailable" | "withdrawn" | "access-unconfirmed";

export type RecipientMedia = LibraryMedia | SearchMedia;

export type OpenMedia = (
  media: RecipientMedia,
  refreshListingAccess: () => Promise<RefreshedMediaAccess>,
) => void;
