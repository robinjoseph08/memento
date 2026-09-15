import { CalendarDays } from "lucide-react";

import { useMediaQuery } from "../../hooks/use-media-query";
import type { ViewerAlbum } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { MediaCounts } from "../albums/media-counts";
import { captureRange } from "./labels";

// The shared Album header: large plain title, uncropped cover beside it on
// desktop and next to the title on phones, then description, range and counts.
// A blurred copy of the cover washes the header in the Album's own colors, so
// each Album's page looks like that Album and not like the next one.
export function AlbumHeader({
  album,
  coverFallback = "No cover",
}: {
  album: ViewerAlbum;
  coverFallback?: string;
}) {
  const desktop = useMediaQuery("(min-width: 761px)");
  const cover = (className: string) => (
    <AlbumImage
      alt="Album cover"
      className={className}
      fallback={coverFallback}
      src={album.cover_preview_url}
    />
  );
  return (
    <header className="relative isolate min-[761px]:grid min-[761px]:grid-cols-[minmax(0,1fr)_minmax(0,390px)] min-[761px]:items-center min-[761px]:gap-12">
      {album.cover_url && (
        <img
          alt=""
          aria-hidden="true"
          className="pointer-events-none absolute -inset-x-10 -inset-y-12 -z-10 size-auto h-[calc(100%+6rem)] w-[calc(100%+5rem)] rounded-3xl [mask-image:radial-gradient(closest-side,black,transparent)] object-cover opacity-30 blur-3xl saturate-150"
          decoding="async"
          src={album.cover_url}
        />
      )}
      <div className="min-w-0">
        <div className="flex items-start gap-5">
          <h1 className="min-w-0 flex-1 font-heading text-[clamp(36px,5vw,64px)] leading-[1.1] tracking-[-1.5px] text-balance">
            {album.title}
          </h1>
          {!desktop && cover("w-28 shrink-0")}
        </div>
        {album.description && (
          <p className="mt-5 max-w-[560px] text-sm text-muted min-[761px]:mt-7">
            {album.description}
          </p>
        )}
        <p className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted">
          <span className="inline-flex items-center gap-1">
            <CalendarDays
              aria-hidden="true"
              className="size-3.5 shrink-0"
              strokeWidth={1.5}
            />
            {captureRange(album) || "No accessible media"}
          </span>
          <MediaCounts
            className="gap-5"
            photos={album.photo_count}
            videos={album.video_count}
          />
        </p>
      </div>
      {desktop && cover("w-full")}
    </header>
  );
}
