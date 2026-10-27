import { Airplay } from "lucide-react";
import { useEffect, useId, useLayoutEffect, type RefObject } from "react";
import { createPortal } from "react-dom";

import { useAirPlay } from "../../hooks/use-airplay";
import { useMediaQuery } from "../../hooks/use-media-query";
import type { Chapter, ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import { aspectRatio, clock } from "./labels";

// The lightbox stage for one video: the native player as large as the stage
// allows at the video's own shape, so a small clip is enlarged to the screen
// instead of sitting at its pixel size, and the player's controls sit on the
// video rather than at the bottom of the screen.
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
  // Tell the browser what is playing. Without this, an Apple TV, the lock
  // screen, and the browser's media controls name the web page instead.
  const title = entry.available ? entry.title || "Video" : "";
  // A video opened by a tap, key, or swipe renders while that gesture is
  // still being handled, and asking it to play now lets it start with sound
  // in browsers that would otherwise wait for another tap, even when its
  // source arrives a moment later. The player stays mounted from video to
  // video, so this runs for each one. Taking away a source does not stop
  // what was playing, so a player still waiting for this video's address
  // lets go of the last one first.
  useLayoutEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    if (!video.getAttribute("src")) video.load();
    if (autoPlay) void video.play()?.catch(() => {});
  }, [autoPlay, entry.id, videoRef]);
  useEffect(() => {
    if (!title || !("mediaSession" in navigator)) return;
    navigator.mediaSession.metadata = new MediaMetadata({ title });
    return () => {
      navigator.mediaSession.metadata = null;
    };
  }, [title]);
  if (!entry.available)
    return (
      <div className="flex h-full w-full items-center justify-center">
        <p className="rounded-sm bg-surface px-6 py-10 text-center text-xs text-muted">
          Media unavailable
        </p>
      </div>
    );
  const ratio = aspectRatio(entry);
  return (
    <div className="[container-type:size] relative flex h-full w-full items-center justify-center">
      <video
        aria-label={entry.title || "Video"}
        autoPlay={autoPlay}
        className="max-h-full max-w-full bg-black object-contain"
        controls
        onTimeUpdate={(event) => onTime(event.currentTarget.currentTime)}
        playsInline
        preload="metadata"
        ref={videoRef}
        src={target.src || undefined}
        style={{
          aspectRatio: ratio,
          width: `min(100cqw, ${ratio} * 100cqh)`,
        }}
        x-webkit-airplay={airPlay ? "allow" : "deny"}
      />
      {target.failed && (
        <p
          className="absolute inset-x-0 top-3 mx-auto w-fit rounded-sm bg-surface px-3 py-2 text-xs"
          role="alert"
        >
          Could not send this video to the TV. Close the video and open it
          again.
        </p>
      )}
      {target.available &&
        actionsTarget &&
        createPortal(
          <Button
            aria-label="AirPlay"
            className="size-11 rounded-full p-0 hover:bg-white/15"
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

// The chapter picker above the player. It names the chapter that is playing,
// follows the video as it plays, and seeks when another one is chosen. A video
// without chapters, whatever the reason, simply has no picker. Its list stops
// partway through a chapter so it plainly scrolls. On a phone it has no
// search, so choosing never raises the keyboard.
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
  const touch = useMediaQuery("(pointer: coarse)");
  if (chapters.length === 0) return null;
  return (
    <div className="flex max-w-full items-center gap-2">
      <span className="text-xs text-muted" id={labelId}>
        Chapter
      </span>
      <Combobox
        aria-labelledby={labelId}
        className="min-h-9 w-56 max-w-full"
        // Four and a half two-line rows, and no more than the screen holds
        // below the search box when there is one.
        listClassName={
          touch
            ? "max-h-[min(13.75rem,var(--radix-popover-content-available-height))]"
            : "max-h-[min(13.75rem,calc(var(--radix-popover-content-available-height)-3rem))]"
        }
        onChange={(value) => onSeek(chapters[Number(value)])}
        options={chapters.map((chapter, index) => ({
          value: String(index),
          label: chapter.title || `Chapter ${index + 1}`,
          description: clock(chapter.start),
        }))}
        placeholder="Choose a chapter"
        search={!touch}
        searchPlaceholder="Search chapters…"
        value={activeIndex >= 0 ? String(activeIndex) : ""}
      />
    </div>
  );
}
