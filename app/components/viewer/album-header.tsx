import { Image, Video } from "lucide-react";

import type { ViewerAlbum } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { captureRange, countLabel } from "./labels";

export function AlbumHeader({ album }: { album: ViewerAlbum }) {
  return (
    <header className="grid grid-cols-[minmax(0,1fr)_112px] items-start gap-x-5 gap-y-5 min-[761px]:grid-cols-[minmax(0,1fr)_minmax(0,390px)] min-[761px]:items-center min-[761px]:gap-x-12">
      <div className="contents min-[761px]:block">
        <h1 className="col-start-1 row-start-1 min-w-0 font-heading text-[clamp(36px,5vw,64px)] leading-[1.1] tracking-[-1.5px] text-balance">
          {album.title}
        </h1>
        {album.description && (
          <p className="col-span-2 max-w-140 text-sm text-muted min-[761px]:mt-7">
            {album.description}
          </p>
        )}
        <p className="col-span-2 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted min-[761px]:mt-4">
          {captureRange(album) && <span>{captureRange(album)}</span>}
          <span className="inline-flex items-center gap-1.5">
            <Image aria-hidden="true" className="size-3.5" strokeWidth={1.5} />
            {countLabel(album.photo_count, "photo")}
          </span>
          <span className="inline-flex items-center gap-1.5">
            <Video aria-hidden="true" className="size-3.5" strokeWidth={1.5} />
            {countLabel(album.video_count, "video")}
          </span>
        </p>
      </div>
      <AlbumImage
        alt="Album cover"
        className="col-start-2 row-start-1 w-full"
        fallback="No cover available"
        src={album.cover_url}
      />
    </header>
  );
}
