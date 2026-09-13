import { useId, type RefObject } from "react";

import type { Chapter, ViewerEntry } from "../../types/generated/publishing";
import { Combobox } from "../ui/combobox";
import { clock } from "./labels";

// The lightbox stage for one video: the native player filling the stage, so a
// small clip is enlarged to the screen instead of sitting at its pixel size.
export function VideoStage({
  entry,
  videoRef,
  onTime,
}: {
  entry: ViewerEntry;
  videoRef: RefObject<HTMLVideoElement | null>;
  onTime: (seconds: number) => void;
}) {
  if (!entry.available)
    return (
      <p className="rounded-sm bg-surface px-6 py-10 text-center text-xs text-muted">
        Media unavailable
      </p>
    );
  return (
    <video
      aria-label={entry.title || "Video"}
      autoPlay
      className="h-full w-full rounded-sm bg-black object-contain"
      controls
      onTimeUpdate={(event) => onTime(event.currentTarget.currentTime)}
      playsInline
      preload="metadata"
      ref={videoRef}
      src={entry.playback_url}
    />
  );
}

// The chapter picker under the player. It names the chapter that is playing,
// follows the video as it plays, and seeks when another one is chosen. A video
// without chapters, whatever the reason, simply has no picker.
export function ChapterSelect({
  entry,
  activeIndex,
  onSeek,
}: {
  entry: ViewerEntry;
  // The chapter that is playing, or -1 before the first one starts.
  activeIndex: number;
  onSeek: (chapter: Chapter) => void;
}) {
  const chapters = entry.chapters;
  const labelId = useId();
  if (chapters.length === 0) return null;
  return (
    <div className="flex max-w-full items-center gap-2">
      <span className="text-xs text-muted" id={labelId}>
        Chapter
      </span>
      <Combobox
        aria-labelledby={labelId}
        className="min-h-9 w-56 max-w-full"
        onChange={(value) => onSeek(chapters[Number(value)])}
        options={chapters.map((chapter, index) => ({
          value: String(index),
          label: chapter.title || `Chapter ${index + 1}`,
          description: clock(chapter.start),
        }))}
        placeholder="Choose a chapter"
        searchPlaceholder="Search chapters…"
        value={activeIndex >= 0 ? String(activeIndex) : ""}
      />
    </div>
  );
}
