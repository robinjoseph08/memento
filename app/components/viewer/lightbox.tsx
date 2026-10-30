import * as DialogPrimitive from "@radix-ui/react-dialog";
import { Cast, ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useLocation, useNavigate, type To } from "react-router-dom";

import { useCast } from "../../hooks/use-cast";
import { useTip } from "../../hooks/use-tip";
import { mediaLabel } from "../../lib/media-labels";
import { cn } from "../../lib/utils";
import type { ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { CastRemote } from "./cast-remote";
import { useIdleControls } from "./idle-controls";
import { aspectRatio, captureDate } from "./labels";
import { DOUBLE_TAP_MS, Pager } from "./pager";
import { PhotoStage, type PhotoZoom } from "./photo-stage";
import { Tip } from "./tip";
import { ChapterSelect, VideoStage } from "./video-player";

// The routed full-screen viewer for one gallery: photos or videos. It composes
// the Radix dialog directly because the shared DialogContent is a centered
// card with its own close button; the primitive still provides Escape, the
// focus trap, and focus return. The URL names the Album Entry, so a reload or
// a shared link lands on the same item; previous and next replace that URL so
// Back returns to the gallery. Entries arrive page by page in the background,
// which is why a link beyond the loaded pages waits instead of failing, and
// only reports the item missing once the gallery is complete. A video mounts
// its player only here, so the gallery never plays anything.
//
// The item fills the screen on black whatever the theme. Its controls float
// over it and fade once someone is just looking: a tap in the middle brings
// them back or hides them, a tap on either side of a photo steps without
// them, and on a computer moving the mouse brings them back.
export function Lightbox({
  kind,
  title,
  total,
  entries,
  currentID,
  loading,
  retry,
  entryLink,
  closeTo,
  personName,
}: {
  kind: "photo" | "video";
  title: string;
  total: number;
  entries: ViewerEntry[];
  currentID: string;
  loading: boolean;
  // retry is set while a later page failed to load, so an item on that page
  // can be tried again from inside the dialog.
  retry?: () => void;
  entryLink: (id: string) => To;
  closeTo: To;
  personName?: string;
}) {
  const noun = kind === "photo" ? "Photo" : "Video";
  const lower = kind === "photo" ? "photo" : "video";
  const navigate = useNavigate();
  const location = useLocation();
  const index = entries.findIndex((entry) => entry.id === currentID);
  const entry = index >= 0 ? entries[index] : undefined;
  const count = Math.max(total, entries.length);
  const stripRef = useRef<HTMLElement>(null);
  const controlsRef = useRef<HTMLDivElement>(null);
  const [stageActions, setStageActions] = useState<HTMLDivElement | null>(null);
  const photoRef = useRef<PhotoZoom>(null);
  // Only the open photo can be zoomed, and stepping always opens one fitted.
  const [zoomedID, setZoomedID] = useState("");
  const zoomed = zoomedID === currentID;
  const trackZoom = useCallback(
    (value: boolean) => setZoomedID(value ? currentID : ""),
    [currentID],
  );
  // The player lives in the stage while the chapter picker sits in the
  // header. Only the playing chapter's index is kept, and only when it
  // changes, so the frequent time updates do not repaint the header.
  const videoRef = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState({ id: "", chapter: -1 });
  const chapterAt = (seconds: number) =>
    (entry?.chapters ?? []).reduce(
      (found, chapter, position) =>
        seconds >= chapter.start ? position : found,
      -1,
    );
  // A video that has not reported time yet is at its start.
  const chapterIndex =
    playing.id === currentID ? playing.chapter : chapterAt(0);
  const trackChapter = (seconds: number) => {
    const index = chapterAt(seconds);
    setPlaying((current) =>
      current.id === currentID && current.chapter === index
        ? current
        : { id: currentID, chapter: index },
    );
  };
  // Curator preview is read only, so it offers neither Cast nor AirPlay.
  const cast = useCast(entry, !personName);
  // A video on the TV does not also play here: a remote control takes the
  // player's place, and chapters and Space reach the TV instead. When casting
  // stops, the player comes back without starting on its own.
  const casting = cast.receiver !== "";
  const [castID, setCastID] = useState("");
  if (casting ? castID !== currentID : castID !== "" && castID !== currentID)
    setCastID(casting ? currentID : "");
  // Space and chapters reach the TV only while it has the open video.
  const onTV = casting && cast.showingID === currentID;
  // Only photos go to the TV without a title, whatever this lightbox shows.
  const castStatus = !cast.showingID
    ? `Connected to ${cast.receiver}`
    : `Showing ${cast.showing || (onTV ? "this photo" : "a photo")} on ${cast.receiver}`;
  // Few players split a video into chapters, so the first video that has them
  // points at the picker. It holds the video still until the Tip is gone,
  // so the Tip is not talking over it, and starts it while the tap that
  // dismissed the Tip is still being handled, which phones require for sound.
  const [picker, setPicker] = useState<HTMLDivElement | null>(null);
  const chaptersTip = useTip("chapters", {
    ready: kind === "video" && !!entry?.available && entry.chapters.length > 0,
  });
  function chaptersSeen() {
    chaptersTip.retire();
    if (castID !== currentID) void videoRef.current?.play()?.catch(() => {});
  }
  function seek(chapter: { start: number }) {
    chaptersTip.retire();
    if (casting) {
      if (onTV) cast.seek(chapter.start);
      return;
    }
    const video = videoRef.current;
    if (!video) return;
    video.currentTime = chapter.start;
    void video.play()?.catch(() => {});
  }
  // Opening from the gallery records which photo was clicked, so closing
  // steps back through history to the gallery rather than adding to it. A
  // direct link has no origin and replaces the lightbox with the gallery.
  const state: unknown = location.state;
  const origin =
    typeof state === "object" && state !== null && "origin" in state
      ? String(state.origin)
      : "";

  // The router applies each URL change as a deferred render, so a second
  // key press or swipe can arrive before the previous one is on screen. Steps
  // count from the last requested photo, not the last rendered one.
  const pendingRef = useRef<string | null>(null);
  useEffect(() => {
    pendingRef.current = null;
  }, [currentID]);
  // The next item renders before the key press, tap, or swipe that asked
  // for it has finished, which is what lets its video start with sound.
  function go(next: ViewerEntry) {
    pendingRef.current = next.id;
    void navigate(entryLink(next.id), {
      replace: true,
      state: location.state,
      flushSync: true,
    });
  }
  function step(delta: number) {
    const from = pendingRef.current ?? currentID;
    const next = entries[entries.findIndex((item) => item.id === from) + delta];
    if (next) go(next);
  }
  // A pull and the close button can both ask before the gallery is back, and
  // stepping back through history twice would leave the gallery too.
  const closingRef = useRef(false);
  function close() {
    if (closingRef.current) return;
    closingRef.current = true;
    if (origin) void navigate(-1);
    else void navigate(closeTo, { replace: true });
  }
  // Closing leaves the gallery at the item that was open last, however far
  // that is from the one that was opened, and focuses it there.
  function returnFocus(event: Event) {
    event.preventDefault();
    for (const id of [currentID, origin]) {
      const target = document.querySelector(
        `[data-entry-id="${CSS.escape(id)}"]`,
      );
      if (target instanceof HTMLElement) {
        const box = target.getBoundingClientRect();
        if (box.top < 0 || box.bottom > window.innerHeight)
          target.scrollIntoView({ block: "center" });
        target.focus({ preventScroll: true });
        return;
      }
    }
  }
  // Keep the current thumbnail in view, also when its page arrives after a
  // direct link. Tiles have fixed widths, so later image loads do not shift it.
  useEffect(() => {
    stripRef.current
      ?.querySelector('[aria-current="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [currentID, entries.length]);
  const label = entry ? `${noun} ${index + 1} of ${count}` : noun;

  // The controls stay up while the pointer rests on one, while one has
  // keyboard focus, while casting, where the phone is the remote, and when
  // there is no item to look at. A video's stay up too: they sit above the
  // player rather than over it, which has controls of its own.
  const [hovering, setHovering] = useState(false);
  const [focusing, setFocusing] = useState(false);
  const controls = useIdleControls(
    kind === "video" || casting || hovering || focusing || !entry,
  );
  const showTimerRef = useRef(0);
  useEffect(() => () => window.clearTimeout(showTimerRef.current), []);
  // A tap hides the controls at once, since that is what a tap does while
  // looking at a photo. Showing them waits out a double tap, which zooms the
  // photo instead.
  function tap() {
    window.clearTimeout(showTimerRef.current);
    if (controls.visible) controls.hide();
    else showTimerRef.current = window.setTimeout(controls.show, DOUBLE_TAP_MS);
  }
  function doubleTap(point: { x: number; y: number }) {
    window.clearTimeout(showTimerRef.current);
    photoRef.current?.toggleAt(point);
  }
  function inControls(target: EventTarget) {
    return target instanceof Node && !!controlsRef.current?.contains(target);
  }

  // A video sits below the controls, so they never cover the player's own.
  // Their height changes as rows wrap, so the stage follows it. The dialog
  // mounts its contents a render late, so this waits for the bar itself.
  const measureBar = useCallback((bar: HTMLDivElement | null) => {
    const content = bar?.closest<HTMLElement>("[role=dialog]");
    if (!bar || !content) return;
    const measure = () =>
      content.style.setProperty(
        "--controls-bottom",
        `${bar.getBoundingClientRect().bottom}px`,
      );
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(bar);
    return () => observer.disconnect();
  }, []);

  // The lightbox reaches under a phone's notch and rounded corners in
  // landscape, where the browser otherwise leaves empty margins. The rest of
  // the app keeps those margins.
  useLayoutEffect(() => {
    const meta = document.querySelector<HTMLMetaElement>(
      'meta[name="viewport"]',
    );
    if (!meta || meta.content.includes("viewport-fit")) return;
    const original = meta.content;
    meta.content = `${original}, viewport-fit=cover`;
    return () => {
      meta.content = original;
    };
  }, []);

  // Each item on the pager's track. Neighbours are drawn beside the open item
  // so a swipe reveals them, and drawing them loads them before they are
  // needed. A photo is the same stage open or not, so one that arrives by a
  // swipe stays on screen instead of loading again. Videos show their
  // thumbnail until they open.
  function slide(item: ViewerEntry, active: boolean) {
    if (kind === "photo")
      return {
        id: item.id,
        node: (
          <PhotoStage
            actionsTarget={active ? stageActions : null}
            active={active}
            alt={mediaLabel(item)}
            key={item.available ? item.preview_url : ""}
            onZoomChange={active ? trackZoom : undefined}
            ref={active ? photoRef : undefined}
            src={item.available ? item.preview_url : ""}
          />
        ),
      };
    if (!active)
      return {
        id: item.id,
        node:
          item.available && item.thumbnail_url ? (
            <div className="h-full w-full pt-(--controls-bottom)">
              <img
                alt=""
                className="h-full w-full object-contain"
                decoding="async"
                draggable={false}
                src={item.thumbnail_url}
              />
            </div>
          ) : null,
      };
    // The open video is one player for the whole lightbox, which plays each
    // video in turn. Phones let a player that has started once with sound
    // start again, where a new one would wait for a tap.
    return {
      id: item.id,
      key: "player",
      node:
        casting && item.available ? (
          <div className="flex h-full w-full items-center justify-center px-3 pt-(--controls-bottom) pb-3">
            <CastRemote
              entry={item}
              failed={cast.failed}
              onPlayOrPause={cast.playOrPause}
              onReplay={cast.replay}
              onSeek={cast.seek}
              onTime={trackChapter}
              ready={onTV}
              receiver={cast.receiver}
            />
          </div>
        ) : (
          <div className="h-full w-full pt-(--controls-bottom)">
            <VideoStage
              actionsTarget={stageActions}
              airPlay={!personName}
              autoPlay={castID !== item.id && !chaptersTip.showing}
              entry={item}
              onTime={trackChapter}
              videoRef={videoRef}
            />
          </div>
        ),
    };
  }

  // Controls stop taking taps while faded, so a tap there reaches the photo.
  const interactive = "pointer-events-auto group-data-idle:pointer-events-none";
  const iconButton = "size-11 rounded-full p-0 hover:bg-white/15";
  const date = entry ? captureDate(entry.captured_at, "short") : "";

  return (
    <DialogPrimitive.Root
      onOpenChange={(open) => {
        if (!open) close();
      }}
      open
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50" />
        <DialogPrimitive.Content
          className="fixed inset-0 z-50 text-foreground outline-none"
          data-theme="dark"
          onBlur={() => setFocusing(false)}
          onCloseAutoFocus={returnFocus}
          onFocus={(event) => {
            if (!inControls(event.target)) return;
            controls.show();
            setFocusing(
              event.target instanceof HTMLElement &&
                event.target.matches(":focus-visible"),
            );
          }}
          onKeyDown={(event) => {
            // A focused player keeps its own arrow keys for seeking and
            // volume, and text fields and an open picker keep theirs.
            if (
              event.target instanceof HTMLElement &&
              event.target.closest(
                'video, input, textarea, [role=combobox][aria-expanded="true"], [role=option]',
              )
            )
              return;
            // Space plays or pauses from anywhere that is not a control of its
            // own, so stepping to the next video with an arrow key never means
            // reaching for the mouse to pause it.
            if (
              kind === "video" &&
              event.key === " " &&
              !(
                event.target instanceof HTMLElement &&
                event.target.closest("button, a")
              )
            ) {
              event.preventDefault();
              if (casting) {
                if (onTV) cast.playOrPause();
                return;
              }
              const video = videoRef.current;
              if (!video) return;
              if (video.paused) void video.play()?.catch(() => {});
              else video.pause();
              return;
            }
            if (event.key === "ArrowLeft") {
              event.preventDefault();
              step(-1);
            }
            if (event.key === "ArrowRight") {
              event.preventDefault();
              step(1);
            }
          }}
          // Focus the stage itself so arrow keys work at once and the close
          // button does not light up on open.
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            if (event.currentTarget instanceof HTMLElement)
              event.currentTarget.focus();
          }}
          // Touching the controls, as when scrolling the filmstrip, keeps them
          // up just as moving the mouse does.
          onPointerDown={(event) => {
            if (inControls(event.target)) controls.show();
          }}
          onPointerLeave={() => setHovering(false)}
          onPointerMove={(event) => {
            if (event.pointerType === "mouse") controls.show();
          }}
          onPointerOver={(event) =>
            setHovering(
              event.pointerType === "mouse" && inControls(event.target),
            )
          }
        >
          {/* Radix names the dialog after its title, so the position is the title. */}
          <DialogPrimitive.Title className="sr-only">
            {label}
          </DialogPrimitive.Title>
          {entry ? (
            <Pager
              current={slide(entry, true)}
              edges={kind === "photo"}
              label={`${noun} stage`}
              locked={zoomed}
              next={entries[index + 1] && slide(entries[index + 1], false)}
              onDismiss={close}
              onDoubleTap={kind === "photo" ? doubleTap : undefined}
              onDragStart={() => {
                window.clearTimeout(showTimerRef.current);
                if (kind === "photo") controls.hide();
              }}
              onStep={step}
              onTap={kind === "photo" ? tap : undefined}
              previous={entries[index - 1] && slide(entries[index - 1], false)}
            />
          ) : (
            <div className="absolute inset-0 flex flex-col items-center justify-center bg-black px-6 text-center">
              {loading ? (
                <p className="text-muted" role="status">
                  Loading {lower}…
                </p>
              ) : (
                <section className="flex flex-col items-center">
                  <h2 className="font-heading text-[27px]/[1.2]">
                    {retry
                      ? `Could not load ${lower}`
                      : `${noun} not available`}
                  </h2>
                  <p className="mt-3 max-w-100 text-sm text-muted" role="alert">
                    {retry
                      ? "Please try again."
                      : personName
                        ? `This ${lower} is not available to ${personName}.`
                        : `This ${lower} is not available to you.`}
                  </p>
                  <div className="mt-6 flex gap-3">
                    {retry && (
                      <Button onClick={retry} variant="outline">
                        Try again
                      </Button>
                    )}
                    <Button onClick={close} variant="outline">
                      Back to {lower}s
                    </Button>
                  </div>
                </section>
              )}
            </div>
          )}
          <div
            className={cn(
              "group pointer-events-none absolute inset-0 transition-opacity duration-200 motion-reduce:transition-none",
              !controls.visible && "opacity-0",
            )}
            data-idle={controls.visible ? undefined : ""}
            ref={controlsRef}
          >
            <header
              className={cn(
                "absolute inset-x-0 top-0 pt-[max(0.5rem,env(safe-area-inset-top))] pr-[max(0.5rem,env(safe-area-inset-right))] pl-[max(0.5rem,env(safe-area-inset-left))] [text-shadow:0_1px_2px_rgb(0_0_0/0.5)] min-[761px]:px-4",
                // Over a photo the controls fade into it; a video sits below.
                kind === "photo"
                  ? "bg-linear-to-b from-black/70 via-black/30 to-transparent pb-8"
                  : "pb-2",
              )}
            >
              <div
                className="flex flex-wrap items-start gap-x-3 gap-y-2"
                ref={measureBar}
              >
                <DialogPrimitive.Close asChild>
                  <Button
                    aria-label={`Close ${lower}`}
                    className={cn(interactive, iconButton)}
                    variant="ghost"
                  >
                    <X
                      aria-hidden="true"
                      className="size-5"
                      strokeWidth={1.5}
                    />
                  </Button>
                </DialogPrimitive.Close>
                <div className="min-w-0 flex-1 pt-1.5">
                  <DialogPrimitive.Description className="truncate font-heading text-lg leading-tight">
                    {kind === "video" && entry ? entry.title || "Video" : title}
                  </DialogPrimitive.Description>
                  <p className="truncate text-xs text-white/75">
                    {entry ? (
                      <>
                        {date && <span>{date}</span>}
                        {date && " · "}
                        <span>{`${index + 1} of ${count}`}</span>
                      </>
                    ) : (
                      " "
                    )}
                  </p>
                </div>
                {personName && (
                  <p className="order-last basis-full text-xs text-white/75 min-[601px]:order-none min-[601px]:basis-auto min-[601px]:self-center">
                    Previewing as {personName}. Read only.
                  </p>
                )}
                {casting && (
                  <div className="order-last flex basis-full items-center gap-2 text-xs text-white/75 min-[601px]:order-none min-[601px]:basis-auto min-[601px]:self-center">
                    {cast.failed ? (
                      <p role="alert">
                        Could not show this {lower} on {cast.receiver}.
                      </p>
                    ) : (
                      <p role="status">{castStatus}</p>
                    )}
                    <Button
                      className={interactive}
                      onClick={cast.stop}
                      size="sm"
                      variant="outline"
                    >
                      Stop casting
                    </Button>
                  </div>
                )}
                {kind === "video" && entry && entry.chapters.length > 0 && (
                  <div className="order-last flex basis-full justify-center [text-shadow:none] min-[601px]:order-none min-[601px]:basis-auto">
                    <div className={interactive} ref={setPicker}>
                      <ChapterSelect
                        activeIndex={chapterIndex}
                        entry={entry}
                        onSeek={seek}
                      />
                    </div>
                    {chaptersTip.showing && picker && (
                      <Tip
                        onDone={chaptersSeen}
                        target={picker}
                        title="This video has chapters"
                      >
                        Use them to jump around to the parts of the video that
                        you care most about.
                      </Tip>
                    )}
                  </div>
                )}
                <div
                  aria-label={`${noun} actions`}
                  className={cn(
                    interactive,
                    "flex shrink-0 items-center gap-1",
                  )}
                  role="group"
                >
                  <div className="contents" ref={setStageActions} />
                  {cast.available && !casting && (
                    <Button
                      aria-label="Cast"
                      className={iconButton}
                      onClick={cast.start}
                      variant="ghost"
                    >
                      <Cast
                        aria-hidden="true"
                        className="size-5"
                        strokeWidth={1.5}
                      />
                    </Button>
                  )}
                  {entry?.download_url ? (
                    <Button
                      aria-label={`Download ${lower}`}
                      asChild
                      className={iconButton}
                      variant="ghost"
                    >
                      <a download href={entry.download_url}>
                        <Download
                          aria-hidden="true"
                          className="size-5"
                          strokeWidth={1.5}
                        />
                      </a>
                    </Button>
                  ) : (
                    <span aria-hidden="true" className="size-11" />
                  )}
                </div>
              </div>
            </header>
            {entry && (
              <>
                <Button
                  aria-label={`Previous ${lower}`}
                  className={cn(
                    interactive,
                    "absolute top-1/2 left-[max(0.75rem,env(safe-area-inset-left))] size-12 -translate-y-1/2 rounded-full bg-black/45 p-0 ring-1 ring-white/10 backdrop-blur-sm hover:bg-black/65 pointer-coarse:sr-only",
                  )}
                  disabled={index === 0}
                  onClick={() => step(-1)}
                  variant="ghost"
                >
                  <ChevronLeft
                    aria-hidden="true"
                    className="size-7"
                    strokeWidth={1.5}
                  />
                </Button>
                <Button
                  aria-label={`Next ${lower}`}
                  className={cn(
                    interactive,
                    "absolute top-1/2 right-[max(0.75rem,env(safe-area-inset-right))] size-12 -translate-y-1/2 rounded-full bg-black/45 p-0 ring-1 ring-white/10 backdrop-blur-sm hover:bg-black/65 pointer-coarse:sr-only",
                  )}
                  disabled={index === entries.length - 1 && !loading}
                  onClick={() => step(1)}
                  variant="ghost"
                >
                  <ChevronRight
                    aria-hidden="true"
                    className="size-7"
                    strokeWidth={1.5}
                  />
                </Button>
              </>
            )}
            {kind === "photo" && entry && (
              <div className="absolute inset-x-0 bottom-0 bg-linear-to-t from-black/70 via-black/30 to-transparent pt-8 pb-[max(0.75rem,env(safe-area-inset-bottom))] [@media(max-height:500px)]:pt-5 [@media(max-height:500px)]:pb-[max(0.375rem,env(safe-area-inset-bottom))]">
                <nav
                  aria-label="Photos filmstrip"
                  className={cn(
                    interactive,
                    "flex [scrollbar-width:none] justify-center-safe gap-0.5 overflow-x-auto pr-[max(0.75rem,env(safe-area-inset-right))] pl-[max(0.75rem,env(safe-area-inset-left))]",
                  )}
                  // A finger scrolling the strip keeps the controls up; the
                  // strip also scrolls itself on every step, which must not.
                  onTouchMove={controls.show}
                  ref={stripRef}
                >
                  {entries.map((item, position) => (
                    <button
                      aria-current={item.id === currentID ? "true" : undefined}
                      aria-label={`Go to ${lower} ${position + 1}`}
                      className="relative h-11 shrink-0 cursor-pointer overflow-hidden rounded-sm bg-surface opacity-60 outline-2 -outline-offset-2 outline-transparent hover:opacity-100 focus-visible:outline-ring aria-[current=true]:opacity-100 aria-[current=true]:outline-primary [@media(max-height:500px)]:h-9"
                      key={item.id}
                      onClick={() => {
                        controls.show();
                        go(item);
                      }}
                      style={{ aspectRatio: aspectRatio(item) }}
                      type="button"
                    >
                      {item.available && (
                        <img
                          alt=""
                          className="h-full w-full object-cover"
                          decoding="async"
                          loading="lazy"
                          src={item.thumbnail_url}
                        />
                      )}
                    </button>
                  ))}
                </nav>
              </div>
            )}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
