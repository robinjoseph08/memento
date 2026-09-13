import { Image, SquarePlay } from "lucide-react";

import { cn } from "../../lib/utils";
import { countLabel } from "./moment-labels";

// Photo and video counts as icons with numbers. The full wording stays in
// hidden text so screen readers and accessible names still say "4 photos, 2 videos".
export function MediaCounts({
  photos,
  videos,
  className,
}: {
  photos: number;
  videos: number;
  className?: string;
}) {
  return (
    <span className={cn("inline-flex items-center gap-3", className)}>
      <span className="sr-only">
        {countLabel(photos, "photo", "photos")},{" "}
        {countLabel(videos, "video", "videos")}
      </span>
      <MediaCount count={photos} hidden kind="photos" />
      <MediaCount count={videos} hidden kind="videos" />
    </span>
  );
}

// One kind on its own, for headings that already name the other dimension.
// With hidden set, the wording is the caller's responsibility.
export function MediaCount({
  kind,
  count,
  hidden = false,
  className,
}: {
  kind: "photos" | "videos";
  count: number;
  hidden?: boolean;
  className?: string;
}) {
  const Icon = kind === "photos" ? Image : SquarePlay;
  const label =
    kind === "photos"
      ? countLabel(count, "photo", "photos")
      : countLabel(count, "video", "videos");
  return (
    <>
      {!hidden && <span className="sr-only">{label}</span>}
      <span
        aria-hidden="true"
        className={cn("inline-flex items-center gap-1", className)}
      >
        <Icon className="size-3.5 shrink-0" strokeWidth={1.5} />
        {count}
      </span>
    </>
  );
}
