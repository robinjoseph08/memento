import { Airplay } from "lucide-react";
import { useId, type RefObject } from "react";
import { createPortal } from "react-dom";

import { useAirPlay } from "../../hooks/use-airplay";
import type { Chapter, ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import { clock } from "./labels";

// The lightbox stage for one video: the native player filling the stage, so a
// small clip is enlarged to the screen instead of sitting at its pixel size.
// With airPlay, Safari may hand the video to an Apple TV, and its button
// joins the header actions in actionsTarget while a target is on the network.
export function VideoStage({
  entry,
  videoRef,
  onTime,
  airPlay,
  autoPlay,
  actionsTarget,
}: {
  entry: ViewerEntry;
  videoRef: RefObject<HTMLVideoElement | null>;
  onTime: (seconds: number) => void;
  airPlay: boolean;
  autoPlay: boolean;
  actionsTarget: HTMLElement | null;
}) {
  const target = useAirPlay(
    videoRef,
    entry.id,
    entry.available ? entry.playback_url : "",
    airPlay,
  );
  if (!entry.available)
    return (
      <p className="rounded-sm bg-surface px-6 py-10 text-center text-xs text-muted">
        Media unavailable
      </p>
    );
  return (
    <div className="relative h-full w-full">
      <video
        aria-label={entry.title || "Video"}
        autoPlay={autoPlay}
        className="h-full w-full rounded-sm bg-black object-contain"
        controls
        onLoadedMetadata={(event) => {
          if (target.resumeAt > 0)
            event.currentTarget.currentTime = target.resumeAt;
        }}
        onTimeUpdate={(event) => onTime(event.currentTarget.currentTime)}
        playsInline
        preload="metadata"
        ref={videoRef}
        src={target.src}
        x-webkit-airplay={airPlay ? "allow" : "deny"}
      />
      {target.failed && (
        <p
          className="absolute inset-x-0 top-3 mx-auto w-fit rounded-sm bg-surface px-3 py-2 text-xs"
          role="alert"
        >
          Could not send this video to the TV. Disconnect AirPlay and try again.
        </p>
      )}
      {target.available &&
        actionsTarget &&
        createPortal(
          <Button
            aria-label="AirPlay"
            className="size-11 rounded-full p-0"
            onClick={target.showPicker}
            variant="ghost"
          >
            <Airplay aria-hidden="true" className="size-5" strokeWidth={1.5} />
          </Button>,
          actionsTarget,
        )}
    </div>
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
