import { CalendarDays } from "lucide-react";

import type { ViewerAlbum } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { MediaCounts } from "../albums/media-counts";
import { countLabel } from "../albums/moment-labels";
import { PrintStack } from "../albums/print-stack";
import { captureRange } from "./labels";

// The viewer's Album tile: square cover, title, counts, and capture range.
// The Album list wraps it in a link; Onboarding shows it as a preview. An
// offered tile, in More albums, for an Album the viewer already has part of
// says how many more photos and videos it holds.
export function AlbumCard({
  album,
  offered = false,
}: {
  album: ViewerAlbum;
  offered?: boolean;
}) {
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
      {offered && album.has_own_media ? (
        <span className="block text-xs/5 text-muted">{moreLabel(album)}</span>
      ) : (
        <MediaCounts
          className="flex text-xs/5 text-muted"
          photos={album.photo_count}
          videos={album.video_count}
        />
      )}
    </>
  );
}

function moreLabel({ photo_count, video_count }: ViewerAlbum) {
  return [
    photo_count > 0 && countLabel(photo_count, "more photo", "more photos"),
    video_count > 0 && countLabel(video_count, "more video", "more videos"),
  ]
    .filter(Boolean)
    .join(" and ");
}
