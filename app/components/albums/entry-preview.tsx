import { SquarePlay } from "lucide-react";
import { useState } from "react";

import { cn } from "../../lib/utils";
import type { Entry } from "../../types/generated/publishing";
import { AlbumImage } from "./album-image";

export function EntryPreview({
  entry,
  cover,
}: {
  entry: Entry;
  cover: boolean;
}) {
  const [ratio, setRatio] = useState(1.5);
  const hour = Number(entry.captured_at.slice(11, 13));
  const clock = `${hour % 12 || 12}:${entry.captured_at.slice(14, 16)}`;
  const period = hour < 12 ? "AM" : "PM";
  const time = `${clock} ${period}`;
  const width = Math.max(40, 112 * ratio);
  const narrow = width < 96;
  return (
    <li className="max-w-full min-w-0 flex-none">
      <figure className="relative max-w-full" style={{ width }}>
        <AlbumImage
          alt={entry.filename}
          className="h-auto w-full rounded-sm"
          fallback={
            entry.available ? "No preview available" : "Unavailable in Immich"
          }
          onError={() => setRatio(1.5)}
          onLoad={({ currentTarget }) =>
            setRatio(currentTarget.naturalWidth / currentTarget.naturalHeight)
          }
          src={entry.available ? entry.thumbnail_url : ""}
        />
        {cover && (
          <span
            className={cn(
              "absolute top-1 rounded-sm bg-black/70 px-1 py-0.5 text-[10px] leading-3 text-white",
              ratio >= 4 ? "left-1/2 -translate-x-1/2" : "left-1",
            )}
            title="Moment cover"
          >
            Cover
          </span>
        )}
        <figcaption className="absolute bottom-1 left-1 rounded-sm bg-black/70 px-0.5 py-0.5 text-[10px] leading-3 whitespace-nowrap text-white">
          <time
            dateTime={entry.captured_at}
            title={`${entry.captured_at.slice(0, 10)} ${time}`}
          >
            {narrow ? (
              <>
                {clock} <br />
                {period}
              </>
            ) : (
              time
            )}
          </time>
        </figcaption>
        {entry.kind === "VIDEO" && (
          <span
            aria-label="Video"
            className={cn(
              "absolute right-1 rounded-sm bg-black/70 p-1 text-white",
              narrow ? "bottom-9" : "bottom-1",
            )}
            role="img"
          >
            <SquarePlay aria-hidden="true" className="size-3.5" />
          </span>
        )}
      </figure>
    </li>
  );
}
