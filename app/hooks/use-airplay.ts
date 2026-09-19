import { useEffect, useState, type RefObject } from "react";

import { signMedia } from "./queries/media";

// Safari's AirPlay surface, which TypeScript's DOM library does not describe.
type AirPlayVideo = HTMLVideoElement & {
  webkitShowPlaybackTargetPicker?: () => void;
  webkitCurrentPlaybackTargetIsWireless?: boolean;
};

// useAirPlay lets Safari hand one video to an Apple TV. The TV has no session
// cookie, so while the video plays there src is a signed URL, and the cookie
// URL returns when it disconnects; resumeAt is where the previous source
// stopped, for the player to seek to once the new one loads. available says
// Safari has found a target, which is when showPicker is worth offering.
// Disabled, or in any other browser, src is always the cookie URL.
export function useAirPlay(
  videoRef: RefObject<HTMLVideoElement | null>,
  entryID: string,
  cookieURL: string,
  enabled: boolean,
) {
  const [signed, setSigned] = useState<{ url: string; resumeAt: number }>();
  const [resumeAt, setResumeAt] = useState(0);
  const [available, setAvailable] = useState(false);
  const [failed, setFailed] = useState(false);
  const mounted = enabled && cookieURL !== "";
  useEffect(() => {
    const video: AirPlayVideo | null = videoRef.current;
    if (!video || !mounted) return;
    const onAvailability = (event: Event) =>
      setAvailable(
        "availability" in event && event.availability === "available",
      );
    // Only the latest change wins, so a quick connect and disconnect cannot
    // leave the signed URL playing locally. Safari may repeat the event when
    // the source changes, so a target that already has its signed URL is
    // left alone.
    let latest = 0;
    let wireless = false;
    const onTarget = () => {
      const next = video.webkitCurrentPlaybackTargetIsWireless === true;
      if (next === wireless) return;
      wireless = next;
      const change = ++latest;
      setFailed(false);
      if (!wireless) {
        setResumeAt(video.currentTime);
        setSigned(undefined);
        return;
      }
      signMedia(entryID, "playback").then(
        (url) => {
          if (change === latest)
            setSigned({ url, resumeAt: video.currentTime });
        },
        () => {
          if (change === latest) setFailed(true);
        },
      );
    };
    video.addEventListener(
      "webkitplaybacktargetavailabilitychanged",
      onAvailability,
    );
    video.addEventListener(
      "webkitcurrentplaybacktargetiswirelesschanged",
      onTarget,
    );
    return () => {
      latest++;
      video.removeEventListener(
        "webkitplaybacktargetavailabilitychanged",
        onAvailability,
      );
      video.removeEventListener(
        "webkitcurrentplaybacktargetiswirelesschanged",
        onTarget,
      );
    };
  }, [videoRef, mounted, entryID]);
  return {
    src: signed?.url ?? cookieURL,
    resumeAt: signed?.resumeAt ?? resumeAt,
    available: mounted && available,
    failed,
    showPicker: () =>
      (
        videoRef.current as AirPlayVideo | null
      )?.webkitShowPlaybackTargetPicker?.(),
  };
}
