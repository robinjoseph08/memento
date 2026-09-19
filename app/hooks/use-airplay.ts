import { useEffect, useState, type RefObject } from "react";

import { signMedia } from "./queries/media";

// Safari's AirPlay surface, which TypeScript's DOM library does not describe.
type AirPlayVideo = HTMLVideoElement & {
  webkitShowPlaybackTargetPicker?: () => void;
  webkitCurrentPlaybackTargetIsWireless?: boolean;
};

// useAirPlay lets Safari hand one video to an Apple TV. The TV has no session
// cookie, so it needs a signed URL, and the source must not change while the
// video is on the TV: that drops the AirPlay route, Safari rebuilds it, and
// the two chase each other. So the signed URL goes in once, as soon as Safari
// reports a target on the network, and stays for the life of the player.
// resumeAt is where the cookie URL had got to, for the player to seek to once
// the signed one loads. available is when showPicker is worth offering, and
// failed says the video reached a TV without a URL the TV can fetch. Disabled,
// or in any other browser, src is always the cookie URL.
export function useAirPlay(
  videoRef: RefObject<HTMLVideoElement | null>,
  entryID: string,
  cookieURL: string,
  enabled: boolean,
) {
  const [signed, setSigned] = useState<{ url: string; resumeAt: number }>();
  const [available, setAvailable] = useState(false);
  const [failed, setFailed] = useState(false);
  const mounted = enabled && cookieURL !== "";
  useEffect(() => {
    const video: AirPlayVideo | null = videoRef.current;
    if (!video || !mounted) return;
    let cancelled = false;
    // A refused mint is tried again the next time Safari reports a target.
    let minting = false;
    let ready = false;
    const onAvailability = (event: Event) => {
      const found =
        "availability" in event && event.availability === "available";
      setAvailable(found);
      if (!found || minting || ready) return;
      minting = true;
      signMedia(entryID, "playback").then(
        (url) => {
          minting = false;
          if (cancelled) return;
          ready = true;
          setFailed(false);
          setSigned({ url, resumeAt: video.currentTime });
        },
        () => {
          minting = false;
        },
      );
    };
    const onTarget = () =>
      setFailed(video.webkitCurrentPlaybackTargetIsWireless === true && !ready);
    video.addEventListener(
      "webkitplaybacktargetavailabilitychanged",
      onAvailability,
    );
    video.addEventListener(
      "webkitcurrentplaybacktargetiswirelesschanged",
      onTarget,
    );
    return () => {
      cancelled = true;
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
    resumeAt: signed?.resumeAt ?? 0,
    available: mounted && available,
    failed,
    showPicker: () =>
      (
        videoRef.current as AirPlayVideo | null
      )?.webkitShowPlaybackTargetPicker?.(),
  };
}
