import { Pause, Play, RotateCcw } from "lucide-react";
import { useEffect, useState } from "react";

import { useCastPlayback } from "../../hooks/use-cast";
import type { ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { clock } from "./labels";

// The lightbox stage for a video that is playing on a TV. The player gives
// way to a remote control, so the video never plays in both places: a poster,
// play and pause, and a seek bar that follows the TV. Dragging the bar moves
// only the handle; the TV seeks once, on release. When the video ends, the
// TV has nothing loaded and the button offers to play it again.
export function CastRemote({
  entry,
  receiver,
  ready,
  onPlayOrPause,
  onReplay,
  onSeek,
  onTime,
}: {
  entry: ViewerEntry;
  receiver: string;
  // Whether the TV has this video yet; until then there is nothing to control.
  ready: boolean;
  onPlayOrPause: () => void;
  onReplay: () => void;
  onSeek: (seconds: number) => void;
  onTime: (seconds: number) => void;
}) {
  const playback = useCastPlayback();
  const [dragging, setDragging] = useState<number>();
  useEffect(() => {
    onTime(playback.currentTime);
  }, [onTime, playback.currentTime]);
  const position = dragging ?? playback.currentTime;
  function release() {
    if (dragging === undefined) return;
    onSeek(dragging);
    setDragging(undefined);
  }
  return (
    <div
      aria-label={`Playing on ${receiver}`}
      className="relative flex h-full w-full items-center justify-center overflow-hidden rounded-sm bg-black"
      role="group"
    >
      <img
        alt=""
        className="absolute inset-0 h-full w-full object-contain opacity-30"
        src={entry.preview_url}
      />
      <div className="relative flex w-full max-w-130 flex-col items-center gap-4 px-6 text-center text-white">
        <p className="text-sm">
          {ready ? `Playing on ${receiver}` : `Sending to ${receiver}`}
        </p>
        {ready && !playback.loaded ? (
          <Button
            aria-label="Play again"
            className="size-14 rounded-full p-0 text-white hover:text-white"
            onClick={onReplay}
            variant="ghost"
          >
            <RotateCcw
              aria-hidden="true"
              className="size-8"
              strokeWidth={1.5}
            />
          </Button>
        ) : (
          <Button
            aria-label={playback.paused ? "Play" : "Pause"}
            className="size-14 rounded-full p-0 text-white hover:text-white"
            disabled={!ready}
            onClick={onPlayOrPause}
            variant="ghost"
          >
            {playback.paused ? (
              <Play aria-hidden="true" className="size-8" strokeWidth={1.5} />
            ) : (
              <Pause aria-hidden="true" className="size-8" strokeWidth={1.5} />
            )}
          </Button>
        )}
        <div className="flex w-full items-center gap-3 text-xs tabular-nums">
          <span>{clock(position)}</span>
          <input
            aria-label="Seek"
            className="min-w-0 flex-1 cursor-pointer accent-primary"
            disabled={!ready || !playback.loaded || playback.duration === 0}
            max={playback.duration}
            min={0}
            onBlur={release}
            onChange={(event) => setDragging(Number(event.target.value))}
            onKeyUp={release}
            onPointerUp={release}
            step="any"
            type="range"
            value={position}
          />
          <span>{clock(playback.duration)}</span>
        </div>
      </div>
    </div>
  );
}
