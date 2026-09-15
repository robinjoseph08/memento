import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, type To } from "react-router-dom";

import { cn } from "../../lib/utils";
import type { ViewerEntry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import { aspectRatio, captureDate } from "./labels";
import { PhotoStage } from "./photo-stage";
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
  const plural = kind === "photo" ? "Photos" : "Videos";
  const navigate = useNavigate();
  const location = useLocation();
  const index = entries.findIndex((entry) => entry.id === currentID);
  const entry = index >= 0 ? entries[index] : undefined;
  const count = Math.max(total, entries.length);
  const stripRef = useRef<HTMLElement>(null);
  const [photoActions, setPhotoActions] = useState<HTMLDivElement | null>(null);
  const swipeRef = useRef<{ x: number; y: number } | null>(null);
  // The player lives in the stage while the chapter picker sits beneath it.
  // Only the playing chapter's index is kept, and only when it changes, so
  // the frequent time updates do not repaint the header and filmstrip.
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
  function seek(chapter: { start: number }) {
    const video = videoRef.current;
    if (!video) return;
    video.currentTime = chapter.start;
    void video.play()?.catch(() => {});
  }
  // Opening from the gallery records which photo was clicked, so closing can
  // hand focus back to it even after moving through many photos. A direct
  // link has no origin, so focus lands on the photo that was open instead.
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
  function go(next: ViewerEntry) {
    pendingRef.current = next.id;
    void navigate(entryLink(next.id), { replace: true, state: location.state });
  }
  function step(delta: number) {
    const from = pendingRef.current ?? currentID;
    const next = entries[entries.findIndex((item) => item.id === from) + delta];
    if (next) go(next);
  }
  function close() {
    if (origin) void navigate(-1);
    else void navigate(closeTo, { replace: true });
  }
  function returnFocus(event: Event) {
    event.preventDefault();
    for (const id of [origin, currentID]) {
      const target = document.querySelector(
        `[data-entry-id="${CSS.escape(id)}"]`,
      );
      if (target instanceof HTMLElement) {
        target.focus();
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
  // Warm the browser cache for neighbouring photos so the next step is
  // instant. Videos stream on demand, so nothing is fetched ahead for them.
  const previous =
    kind === "photo" ? (entries[index - 1]?.preview_url ?? "") : "";
  const following =
    kind === "photo" ? (entries[index + 1]?.preview_url ?? "") : "";
  useEffect(() => {
    for (const url of [previous, following]) {
      if (url) new Image().src = url;
    }
  }, [previous, following]);

  const label = entry ? `${noun} ${index + 1} of ${count}` : noun;

  return (
    <DialogPrimitive.Root
      onOpenChange={(open) => {
        if (!open) close();
      }}
      open
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-background" />
        <DialogPrimitive.Content
          className="fixed inset-0 z-50 flex flex-col text-foreground outline-none"
          onCloseAutoFocus={returnFocus}
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
        >
          {/* Radix names the dialog after its title, so the position is the title. */}
          <DialogPrimitive.Title className="sr-only">
            {label}
          </DialogPrimitive.Title>
          <header className="flex flex-wrap items-start gap-3 px-3 pt-3 min-[761px]:px-5">
            <DialogPrimitive.Close asChild>
              <Button
                aria-label={`Close ${lower}`}
                className="size-11 rounded-full p-0"
                variant="ghost"
              >
                <X aria-hidden="true" className="size-5" strokeWidth={1.5} />
              </Button>
            </DialogPrimitive.Close>
            <div className="min-w-0 flex-1 pt-1.5">
              <DialogPrimitive.Description className="truncate font-heading text-lg leading-tight">
                {title}
              </DialogPrimitive.Description>
              <p className="text-xs text-muted">
                {entry ? `${index + 1} of ${count}` : " "}
              </p>
            </div>
            {personName && (
              <p className="order-last basis-full text-xs text-muted min-[601px]:order-none min-[601px]:basis-auto min-[601px]:self-center">
                Previewing as {personName}. Read only.
              </p>
            )}
            <div
              aria-label={`${noun} actions`}
              className="flex shrink-0 items-center gap-1"
              role="group"
            >
              <div className="contents" ref={setPhotoActions} />
              {entry?.download_url ? (
                <Button
                  aria-label={`Download ${lower}`}
                  asChild
                  className="size-11 rounded-full p-0"
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
          </header>
          {entry ? (
            <>
              <div
                aria-label={`${noun} stage`}
                className="relative flex min-h-0 flex-1 [touch-action:pan-y_pinch-zoom] items-center justify-center px-14 py-3 select-none"
                onPointerCancel={() => {
                  swipeRef.current = null;
                }}
                onPointerDown={(event) => {
                  swipeRef.current =
                    kind === "video" && event.pointerType === "touch"
                      ? { x: event.clientX, y: event.clientY }
                      : null;
                }}
                onPointerUp={(event) => {
                  const start = swipeRef.current;
                  swipeRef.current = null;
                  if (!start || event.pointerType !== "touch") return;
                  const dx = event.clientX - start.x;
                  const dy = event.clientY - start.y;
                  if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy))
                    step(dx < 0 ? 1 : -1);
                }}
                role="group"
              >
                <Button
                  aria-label={`Previous ${lower}`}
                  className="absolute top-1/2 left-2 size-11 -translate-y-1/2 rounded-full p-0"
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
                <div
                  className="flex h-full w-full items-center justify-center"
                  key={entry.id}
                >
                  {kind === "video" ? (
                    <VideoStage
                      entry={entry}
                      onTime={trackChapter}
                      videoRef={videoRef}
                    />
                  ) : (
                    <PhotoStage
                      actionsTarget={photoActions}
                      alt={entry.title || "Photo"}
                      key={entry.available ? entry.preview_url : ""}
                      onStep={step}
                      src={entry.available ? entry.preview_url : ""}
                    />
                  )}
                </div>
                <Button
                  aria-label={`Next ${lower}`}
                  className="absolute top-1/2 right-2 size-11 -translate-y-1/2 rounded-full p-0"
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
              </div>
              {kind === "video" ? (
                <div className="flex flex-col items-center gap-2 px-3 pb-3 text-center">
                  <p className="font-heading text-lg leading-tight">
                    {entry.title || "Video"}
                  </p>
                  <ChapterSelect
                    activeIndex={chapterIndex}
                    entry={entry}
                    onSeek={seek}
                  />
                  <p className="text-xs text-muted">
                    {captureDate(entry.captured_at, true)}
                  </p>
                </div>
              ) : (
                <p className="pb-3 text-center text-xs text-muted">
                  {captureDate(entry.captured_at, true)}
                </p>
              )}
              <nav
                aria-label={`${plural} filmstrip`}
                className="flex justify-center-safe gap-0.5 overflow-x-auto px-3 pb-4"
                ref={stripRef}
              >
                {entries.map((item, position) => (
                  <button
                    aria-current={item.id === currentID ? "true" : undefined}
                    aria-label={`Go to ${lower} ${position + 1}`}
                    className={cn(
                      "relative h-12 shrink-0 cursor-pointer overflow-hidden rounded-sm bg-surface opacity-60 outline-2 -outline-offset-2 outline-transparent hover:opacity-100 focus-visible:outline-ring aria-[current=true]:opacity-100 aria-[current=true]:outline-primary",
                    )}
                    key={item.id}
                    onClick={() => go(item)}
                    style={{ width: Math.round(48 * aspectRatio(item)) }}
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
            </>
          ) : loading ? (
            <p
              className="flex flex-1 items-center justify-center text-muted"
              role="status"
            >
              Loading {lower}…
            </p>
          ) : (
            <section className="flex flex-1 flex-col items-center justify-center px-6 text-center">
              <h2 className="font-heading text-[27px]/[1.2]">
                {retry ? `Could not load ${lower}` : `${noun} not available`}
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
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
