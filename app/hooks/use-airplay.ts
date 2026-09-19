import { useEffect, useState, type RefObject } from "react";

import { signMedia } from "./queries/media";

// Safari's AirPlay surface, which TypeScript's DOM library does not describe.
type AirPlayVideo = HTMLVideoElement & {
  webkitShowPlaybackTargetPicker?: () => void;
  webkitCurrentPlaybackTargetIsWireless?: boolean;
};

// useAirPlay lets Safari hand one video to an Apple TV. The TV has no session
// cookie, so it needs a signed URL, and the source must not change once the
// video is on the TV: that drops the AirPlay route, Safari rebuilds it, and
// the two chase each other. So where AirPlay exists the video plays a signed
// URL from the start, and src stays empty for the moment it takes to mint
// one. If none can be had, src is the cookie URL and failed says when that
// video has reached a TV that cannot fetch it. available is when showPicker
// is worth offering. Disabled, or in any other browser, src is the cookie URL.
export function useAirPlay(
  videoRef: RefObject<HTMLVideoElement | null>,
  entryID: string,
  cookieURL: string,
  enabled: boolean,
) {
  const capable =
    enabled &&
    cookieURL !== "" &&
    "WebKitPlaybackTargetAvailabilityEvent" in window;
  const [signed, setSigned] = useState<{
    entryID: string;
    url: string | null;
  }>();
  const [wireless, setWireless] = useState(false);
  const [available, setAvailable] = useState(false);
  useEffect(() => {
    if (!capable) return;
    let cancelled = false;
    signMedia(entryID, "playback").then(
      (url) => {
        if (!cancelled) setSigned({ entryID, url });
      },
      () => {
        if (!cancelled) setSigned({ entryID, url: null });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [capable, entryID]);
  useEffect(() => {
    const video: AirPlayVideo | null = videoRef.current;
    if (!video || !capable) return;
    const onAvailability = (event: Event) =>
      setAvailable(
        "availability" in event && event.availability === "available",
      );
    const onTarget = () =>
      setWireless(video.webkitCurrentPlaybackTargetIsWireless === true);
    video.addEventListener(
      "webkitplaybacktargetavailabilitychanged",
      onAvailability,
    );
    video.addEventListener(
      "webkitcurrentplaybacktargetiswirelesschanged",
      onTarget,
    );
    return () => {
      video.removeEventListener(
        "webkitplaybacktargetavailabilitychanged",
        onAvailability,
      );
      video.removeEventListener(
        "webkitcurrentplaybacktargetiswirelesschanged",
        onTarget,
      );
    };
  }, [videoRef, capable]);
  // Undefined while minting, null when no signed URL could be had.
  const minted = signed?.entryID === entryID ? signed.url : undefined;
  return {
    src: !capable || minted === null ? cookieURL : (minted ?? ""),
    available: capable && available,
    failed: capable && wireless && minted === null,
    showPicker: () =>
      (
        videoRef.current as AirPlayVideo | null
      )?.webkitShowPlaybackTargetPicker?.(),
  };
}
