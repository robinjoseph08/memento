import { Check, SquarePlay } from "lucide-react";
import { useState } from "react";

import { cn } from "../../lib/utils";
import type { Entry } from "../../types/generated/publishing";
import { AlbumImage } from "./album-image";

export function EntryPreview({
  entry,
  cover,
  selected = false,
  onSelect,
  onInspect,
}: {
  entry: Entry;
  cover: boolean;
  selected?: boolean;
  onSelect?: (selected: boolean) => void;
  onInspect?: () => void;
}) {
  const [ratio, setRatio] = useState(1.5);
  const hour = Number(entry.captured_at.slice(11, 13));
  const clock = `${hour % 12 || 12}:${entry.captured_at.slice(14, 16)}`;
  const period = hour < 12 ? "AM" : "PM";
  const time = `${clock} ${period}`;
  const width = Math.max(64, 112 * ratio);
  const narrow = width < 96;
  const image = (
    <AlbumImage
      alt={entry.filename}
      className={cn(
        "h-auto w-full rounded-sm",
        selected && "outline-2 outline-offset-2 outline-primary",
      )}
      fallback={
        entry.available ? "No preview available" : "Unavailable in Immich"
      }
      onError={() => setRatio(1.5)}
      onLoad={({ currentTarget }) =>
        setRatio(currentTarget.naturalWidth / currentTarget.naturalHeight)
      }
      src={entry.available ? entry.thumbnail_url : ""}
    />
  );
  return (
    <li className="max-w-full min-w-0 flex-none" style={{ width }}>
      <figure className="relative max-w-full">
        {onSelect ? (
          <label className="block cursor-pointer touch-manipulation">
            <input
              aria-label={`Select ${entry.filename}`}
              checked={selected}
              className="peer absolute top-1 left-1 z-10 size-5 cursor-pointer appearance-none rounded-sm border border-white/70 bg-black/65 checked:border-primary checked:bg-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              onChange={(event) => onSelect(event.target.checked)}
              type="checkbox"
            />
            <Check
              aria-hidden="true"
              className="pointer-events-none absolute top-1.5 left-1.5 z-20 hidden size-4 text-primary-foreground peer-checked:block"
              strokeWidth={2.5}
            />
            {image}
          </label>
        ) : (
          image
        )}
        {cover && (
          <span
            className={cn(
              "absolute top-1 rounded-sm bg-black/70 px-1 py-0.5 text-[10px] leading-3 text-white",
              onSelect
                ? "left-8"
                : ratio >= 4
                  ? "left-1/2 -translate-x-1/2"
                  : "left-1",
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
            {time}
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
            title="Video"
          >
            <SquarePlay aria-hidden="true" className="size-3.5" />
          </span>
        )}
      </figure>
      {onInspect && (
        <button
          aria-label={`Inspect ${entry.filename}`}
          className="mt-1 flex w-full cursor-pointer justify-end text-[10px] text-muted hover:text-foreground focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-ring"
          onClick={onInspect}
          type="button"
        >
          Details
        </button>
      )}
    </li>
  );
}
