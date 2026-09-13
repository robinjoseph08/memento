import { Image, SquarePlay } from "lucide-react";

import { cn } from "../../lib/utils";
import { countLabel } from "./moment-labels";

// Photo and video counts side by side as icons with numbers. The full wording
// stays in hidden text so screen readers and accessible names still say
// "4 photos, 2 videos", and each pair carries it as a tooltip for pointers.
// A single count on its own stays plain text; use countLabel for that.
export function MediaCounts({
  photos,
  videos,
  className,
}: {
  photos: number;
  videos: number;
  className?: string;
}) {
  const photoLabel = countLabel(photos, "photo", "photos");
  const videoLabel = countLabel(videos, "video", "videos");
  return (
    <span className={cn("inline-flex items-center gap-3", className)}>
      <span className="sr-only">
        {photoLabel}, {videoLabel}
      </span>
      <span
        aria-hidden="true"
        className="inline-flex items-center gap-1"
        title={photoLabel}
      >
        <Image className="size-3.5 shrink-0" strokeWidth={1.5} />
        {photos}
      </span>
      <span
        aria-hidden="true"
        className="inline-flex items-center gap-1"
        title={videoLabel}
      >
        <SquarePlay className="size-3.5 shrink-0" strokeWidth={1.5} />
        {videos}
      </span>
    </span>
  );
}
