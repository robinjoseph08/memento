import { useEffect, useSyncExternalStore } from "react";

import {
  castFailed,
  castSnapshot,
  connectCast,
  showOnCast,
  startCast,
  stopCast,
  subscribeCast,
} from "../lib/cast";
import { contentType } from "../lib/http";
import type { ViewerEntry } from "../types/generated/publishing";
import { signMedia } from "./queries/media";

// Only the newest request reaches the receiver, so stepping quickly through
// a lightbox never leaves an earlier item on the TV.
let latest = 0;

async function show(entry: ViewerEntry) {
  const request = ++latest;
  const video = entry.kind === "VIDEO";
  try {
    // The receiver needs the real content type, which only the media route
    // knows, so ask it without downloading anything.
    const [url, type] = await Promise.all([
      signMedia(entry.id, video ? "playback" : "preview"),
      contentType(video ? entry.playback_url : entry.preview_url),
    ]);
    if (request !== latest) return;
    await showOnCast({
      id: entry.id,
      title: entry.title || (video ? "Video" : "Photo"),
      url,
      contentType: type ?? (video ? "video/mp4" : "image/jpeg"),
    });
  } catch {
    if (request === latest) castFailed();
  }
}

// useCast gives a lightbox its Google Cast controls. While a receiver is
// connected, whichever entry the lightbox has open is shown on it, so next
// and previous move the TV too. Closing the lightbox leaves the session
// alone; only stop ends it. Disabled, it reports nothing and casts nothing.
export function useCast(entry: ViewerEntry | undefined, enabled: boolean) {
  const state = useSyncExternalStore(subscribeCast, castSnapshot);
  useEffect(() => {
    if (enabled) connectCast();
  }, [enabled]);
  const connected = enabled && state.receiver !== "";
  const castable = entry?.available ? entry : undefined;
  useEffect(() => {
    if (connected && castable && castSnapshot().showingID !== castable.id)
      void show(castable);
  }, [connected, castable]);
  return {
    available: enabled && state.available,
    receiver: connected ? state.receiver : "",
    showing: state.showingTitle,
    failed: state.failed,
    start: startCast,
    stop: stopCast,
  };
}
