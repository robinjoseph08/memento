import type { ViewerAlbum } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { countLabel } from "../albums/moment-labels";
import { captureRange } from "./labels";

// The viewer's Album tile: square cover, title, counts, and capture range.
// The Album list wraps it in a link; Onboarding shows it as a preview.
export function AlbumCard({ album }: { album: ViewerAlbum }) {
  return (
    <>
      <AlbumImage
        alt={album.title}
        className="aspect-square w-full object-cover"
        fallback="No cover"
        src={album.cover_url}
      />
      <span className="mt-3 block font-heading text-lg wrap-anywhere">
        {album.title}
      </span>
      <span className="block text-xs text-muted">
        {countLabel(album.photo_count, "photo", "photos")},{" "}
        {countLabel(album.video_count, "video", "videos")}
      </span>
      <span className="block text-xs text-muted">{captureRange(album)}</span>
    </>
  );
}
