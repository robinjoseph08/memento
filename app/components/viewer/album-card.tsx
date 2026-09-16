import { CalendarDays } from "lucide-react";

import type { ViewerAlbum } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { MediaCounts } from "../albums/media-counts";
import { PrintStack } from "../albums/print-stack";
import { captureRange } from "./labels";

// The viewer's Album tile: square cover, title, counts, and capture range.
// The Album list wraps it in a link; Onboarding shows it as a preview.
export function AlbumCard({ album }: { album: ViewerAlbum }) {
  return (
    <>
      <PrintStack>
        <AlbumImage
          alt={album.title}
          className="aspect-square w-full object-cover"
          fallback="No cover"
          src={album.cover_url}
        />
      </PrintStack>
      <span className="mt-3 block font-heading text-lg wrap-anywhere">
        {album.title}
      </span>
      <span className="mt-1 flex items-start gap-1 text-xs/5 text-muted">
        <CalendarDays
          aria-hidden="true"
          className="mt-[3px] size-3.5 shrink-0"
          strokeWidth={1.5}
        />
        {captureRange(album)}
      </span>
      <MediaCounts
        className="flex text-xs/5 text-muted"
        photos={album.photo_count}
        videos={album.video_count}
      />
    </>
  );
}
