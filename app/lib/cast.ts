/// <reference types="chromecast-caf-sender" />

// The adapter around Google's Cast sender SDK. The SDK is one global per page,
// so its state is kept here as one store that React reads through
// subscribeCast and castSnapshot. Nothing is persisted: a reload starts over.

const sdkURL =
  "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1";

export type CastSnapshot = {
  // Whether the SDK has found at least one receiver on the network.
  available: boolean;
  // The connected receiver's name, or empty while nothing is connected.
  receiver: string;
  // What the receiver is showing, as the Album Entry and its title. Photos
  // have no title, so showingID alone says that something is showing.
  showingID: string;
  showingTitle: string;
  // Whether the last attempt to show something on the receiver failed.
  failed: boolean;
};

export type CastItem = {
  id: string;
  title: string;
  url: string;
  contentType: string;
};

const idle: CastSnapshot = {
  available: false,
  receiver: "",
  showingID: "",
  showingTitle: "",
  failed: false,
};
let snapshot = idle;
const listeners = new Set<() => void>();

function publish(next: CastSnapshot) {
  snapshot = next;
  for (const listener of listeners) listener();
}

export function subscribeCast(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function castSnapshot() {
  return snapshot;
}

// Playback on the receiver ticks every second, so it has a store of its own:
// only the remote control repaints with it, not the whole lightbox.
export type CastPlayback = {
  // Whether the receiver reports media. It starts false, so on its own it
  // cannot tell "not yet" from "over".
  loaded: boolean;
  // Whether media was loaded and then went away, which is how a video that
  // played to its end looks. Never true just because nothing was reported.
  ended: boolean;
  paused: boolean;
  currentTime: number;
  duration: number;
};

const stopped: CastPlayback = {
  loaded: false,
  ended: false,
  paused: false,
  currentTime: 0,
  duration: 0,
};
let playback = stopped;
const playbackListeners = new Set<() => void>();
let remote:
  | {
      player: cast.framework.RemotePlayer;
      controller: cast.framework.RemotePlayerController;
    }
  | undefined;

function publishPlayback(next: CastPlayback) {
  if (
    next.loaded === playback.loaded &&
    next.ended === playback.ended &&
    next.paused === playback.paused &&
    next.currentTime === playback.currentTime &&
    next.duration === playback.duration
  )
    return;
  playback = next;
  for (const listener of playbackListeners) listener();
}

export function subscribeCastPlayback(listener: () => void) {
  playbackListeners.add(listener);
  return () => {
    playbackListeners.delete(listener);
  };
}

export function castPlayback() {
  return playback;
}

// The SDK and its remote player bind to the page once, before any session
// exists. A hot update would run this module again beside a live session and
// leave the remote player deaf, so an edit here reloads the page instead.
if (import.meta.hot) import.meta.hot.accept(() => window.location.reload());

let requested = false;

// connectCast loads the SDK once. Only Chromium browsers can cast, so others
// never fetch Google's script.
export function connectCast() {
  if (requested || !("chrome" in window)) return;
  requested = true;
  window.__onGCastApiAvailable = (available) => {
    if (available) watch();
  };
  const script = document.createElement("script");
  script.src = sdkURL;
  script.async = true;
  document.head.append(script);
}

function watch() {
  const context = cast.framework.CastContext.getInstance();
  context.setOptions({
    receiverApplicationId: chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
    autoJoinPolicy: chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED,
  });
  const sync = () => {
    const state = context.getCastState();
    const session = context.getCurrentSession();
    if (state !== cast.framework.CastState.CONNECTED || !session) {
      publishPlayback(stopped);
      publish({
        ...idle,
        available: state !== cast.framework.CastState.NO_DEVICES_AVAILABLE,
      });
      return;
    }
    // A session joined after a reload is already showing something. Naming it
    // keeps the lightbox from loading the same item again from the start.
    const joined = snapshot.receiver === "" ? showing(session) : {};
    publish({
      ...snapshot,
      ...joined,
      available: true,
      receiver: session.getCastDevice().friendlyName,
    });
  };
  context.addEventListener(
    cast.framework.CastContextEventType.CAST_STATE_CHANGED,
    sync,
  );
  sync();
  const player = new cast.framework.RemotePlayer();
  const controller = new cast.framework.RemotePlayerController(player);
  remote = { player, controller };
  controller.addEventListener(
    cast.framework.RemotePlayerEventType.ANY_CHANGE,
    () =>
      publishPlayback({
        loaded: player.isMediaLoaded,
        ended: !player.isMediaLoaded && (playback.loaded || playback.ended),
        paused: player.isPaused,
        currentTime: player.currentTime,
        duration: player.duration,
      }),
  );
}

// showing reads back what showOnCast recorded on the receiver's current media.
function showing(session: cast.framework.CastSession) {
  const data: unknown = session.getMediaSession()?.media?.customData;
  if (typeof data !== "object" || data === null) return {};
  if (!("entryID" in data) || !("title" in data)) return {};
  return typeof data.entryID === "string" && typeof data.title === "string"
    ? { showingID: data.entryID, showingTitle: data.title }
    : {};
}

// startCast opens Chrome's receiver picker. Dismissing it rejects, which is
// not a failure worth reporting.
export function startCast() {
  void cast.framework.CastContext.getInstance()
    .requestSession()
    .catch(() => {});
}

// stopCast ends the session and clears the TV. It is safe to call when
// nothing is connected, including before the SDK has loaded.
export function stopCast() {
  if (snapshot.receiver)
    cast.framework.CastContext.getInstance().endCurrentSession(true);
}

export function playOrPauseCast() {
  remote?.controller.playOrPause();
}

export function seekCast(seconds: number) {
  if (!remote) return;
  remote.player.currentTime = seconds;
  remote.controller.seek();
  // The TV confirms up to a second later; until then the bar holds the target.
  publishPlayback({ ...playback, currentTime: seconds });
}

// showOnCast replaces whatever the connected receiver is showing.
export async function showOnCast(item: CastItem) {
  const session = cast.framework.CastContext.getInstance().getCurrentSession();
  if (!session) return;
  const media = new chrome.cast.media.MediaInfo(item.url, item.contentType);
  const metadata = new chrome.cast.media.GenericMediaMetadata();
  if (item.title) metadata.title = item.title;
  media.metadata = metadata;
  media.customData = { entryID: item.id, title: item.title };
  // The remote starts over with each item, never showing the last one's time.
  publishPlayback(stopped);
  await session.loadMedia(new chrome.cast.media.LoadRequest(media));
  if (snapshot.receiver)
    publish({
      ...snapshot,
      showingID: item.id,
      showingTitle: item.title,
      failed: false,
    });
}

// castFailed records that the current item could not be shown, so the
// lightbox never claims the TV shows something it does not.
export function castFailed() {
  if (snapshot.receiver)
    publish({ ...snapshot, showingID: "", showingTitle: "", failed: true });
}
