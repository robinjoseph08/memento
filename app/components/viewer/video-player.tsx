import { ListVideo } from "lucide-react";
import { useRef, useState } from "react";

import { cn } from "../../lib/utils";
import type { Chapter, ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { clock } from "./labels";

// The lightbox stage for one video: the chapter control above the native
// player. Chapters open a compact temporary popover; choosing one seeks and
// closes it. No chapters and failed extraction are plain labels, never a
// warning or a control that does nothing.
export function VideoPlayer({ entry }: { entry: ViewerEntry }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [open, setOpen] = useState(false);
  const [time, setTime] = useState(0);
  const chapters = entry.chapters;
  const activeIndex = chapters.reduce(
    (found, chapter, index) => (time >= chapter.start ? index : found),
    -1,
  );
  function seek(chapter: Chapter) {
    const video = videoRef.current;
    if (video) {
      video.currentTime = chapter.start;
      void video.play()?.catch(() => {});
    }
    setOpen(false);
  }
  return (
    <div className="relative flex h-full w-full flex-col items-center justify-center gap-3">
      <div className="flex min-h-8 items-center">
        {chapters.length > 0 ? (
          <Popover onOpenChange={setOpen} open={open}>
            <PopoverTrigger asChild>
              <Button className="rounded-full" size="sm" variant="outline">
                <ListVideo
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
                Chapters
              </Button>
            </PopoverTrigger>
            <PopoverContent
              align="center"
              aria-label="Chapters"
              className="max-h-[min(70vh,24rem)] overflow-y-auto p-1"
              // Open on the playing chapter so Enter continues from there.
              onOpenAutoFocus={(event) => {
                event.preventDefault();
                const buttons =
                  event.currentTarget instanceof HTMLElement
                    ? event.currentTarget.querySelectorAll("button")
                    : undefined;
                buttons?.[Math.max(activeIndex, 0)]?.focus();
              }}
            >
              <ul>
                {chapters.map((chapter, index) => (
                  <li key={`${chapter.start}-${chapter.end}-${chapter.title}`}>
                    <button
                      aria-current={index === activeIndex ? "true" : undefined}
                      className={cn(
                        "flex w-full cursor-pointer items-baseline justify-between gap-3 rounded-sm px-2 py-2 text-left text-sm hover:bg-surface focus-visible:bg-surface focus-visible:outline-none",
                        index === activeIndex &&
                          "bg-primary/15 text-accent-foreground",
                      )}
                      onClick={() => seek(chapter)}
                      type="button"
                    >
                      <span className="min-w-0 truncate">
                        {chapter.title || `Chapter ${index + 1}`}
                      </span>
                      <span className="shrink-0 text-xs text-muted tabular-nums">
                        {clock(chapter.start)}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </PopoverContent>
          </Popover>
        ) : (
          <p className="text-xs text-muted">
            {entry.chapter_status === "failed"
              ? "Chapters unavailable"
              : entry.chapter_status === "complete"
                ? "No chapters"
                : ""}
          </p>
        )}
      </div>
      <div className="flex min-h-0 w-full flex-1 items-center justify-center">
        {entry.available ? (
          <video
            aria-label={entry.title || "Video"}
            autoPlay
            className="max-h-full max-w-full rounded-sm bg-black"
            controls
            onTimeUpdate={(event) => setTime(event.currentTarget.currentTime)}
            playsInline
            preload="metadata"
            ref={videoRef}
            src={entry.playback_url}
          />
        ) : (
          <p className="rounded-sm bg-surface px-6 py-10 text-center text-xs text-muted">
            Media unavailable
          </p>
        )}
      </div>
    </div>
  );
}
