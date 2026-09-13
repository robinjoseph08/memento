import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useLocation, useNavigate, type To } from "react-router-dom";

import { cn } from "../../lib/utils";
import type { ViewerEntry } from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { Button } from "../ui/button";
import { aspectRatio, captureDate } from "./labels";

// The routed full-screen photo viewer. It composes the Radix dialog directly
// because the shared DialogContent is a centered card with its own close
// button; the primitive still provides Escape, the focus trap, and focus
// return. The URL names the Album Entry, so a
// reload or a shared link lands on the same photo; previous and next replace
// that URL so Back returns to the gallery. Photos arrive page by page in the
// background, which is why a link beyond the loaded pages waits instead of
// failing, and only reports the photo missing once the gallery is complete.
export function Lightbox({
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
  title: string;
  total: number;
  entries: ViewerEntry[];
  currentID: string;
  loading: boolean;
  // retry is set while a later page failed to load, so a photo on that page
  // can be tried again from inside the dialog.
  retry?: () => void;
  entryLink: (id: string) => To;
  closeTo: To;
  personName?: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const index = entries.findIndex((entry) => entry.id === currentID);
  const entry = index >= 0 ? entries[index] : undefined;
  const count = Math.max(total, entries.length);
  const stripRef = useRef<HTMLElement>(null);
  const swipeRef = useRef<{ x: number; y: number } | null>(null);
  // Opening from the gallery records which photo was clicked, so closing can
  // hand focus back to it even after moving through many photos. A direct
  // link has no origin, so focus lands on the photo that was open instead.
  const state: unknown = location.state;
  const origin =
    typeof state === "object" && state !== null && "origin" in state
      ? String(state.origin)
      : "";

  function go(next: ViewerEntry) {
    void navigate(entryLink(next.id), { replace: true, state: location.state });
  }
  function step(delta: number) {
    const next = entries[index + delta];
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
  // Warm the browser cache for the neighbours so the next step is instant.
  const previous = entries[index - 1]?.preview_url ?? "";
  const following = entries[index + 1]?.preview_url ?? "";
  useEffect(() => {
    for (const url of [previous, following]) {
      if (url) new Image().src = url;
    }
  }, [previous, following]);

  const label = entry ? `Photo ${index + 1} of ${count}` : "Photo";

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
                aria-label="Close photo"
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
              <p className="order-last basis-full text-xs text-muted min-[601px]:order-none min-[601px]:basis-auto min-[601px]:pt-1.5">
                Previewing as {personName}. Read only.
              </p>
            )}
            {entry?.download_url ? (
              <Button
                aria-label="Download photo"
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
          </header>
          {entry ? (
            <>
              <div
                aria-label="Photo stage"
                className="relative flex min-h-0 flex-1 [touch-action:pan-y_pinch-zoom] items-center justify-center px-14 py-3 select-none"
                onPointerCancel={() => {
                  swipeRef.current = null;
                }}
                onPointerDown={(event) => {
                  swipeRef.current =
                    event.pointerType === "touch"
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
                  aria-label="Previous photo"
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
                  <AlbumImage
                    alt={entry.title || "Photo"}
                    className="h-full w-auto max-w-full bg-transparent object-contain"
                    fallback="Media unavailable"
                    src={entry.available ? entry.preview_url : ""}
                  />
                </div>
                <Button
                  aria-label="Next photo"
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
              <p className="pb-3 text-center text-xs text-muted">
                {captureDate(entry.captured_at, true)}
              </p>
              <nav
                aria-label="Photos in album"
                className="flex justify-center-safe gap-0.5 overflow-x-auto px-3 pb-4"
                ref={stripRef}
              >
                {entries.map((item, position) => (
                  <button
                    aria-current={item.id === currentID ? "true" : undefined}
                    aria-label={`Go to photo ${position + 1}`}
                    className={cn(
                      "relative h-12 shrink-0 cursor-pointer overflow-hidden rounded-sm bg-surface opacity-60 outline-2 outline-offset-0 outline-transparent hover:opacity-100 focus-visible:outline-ring aria-[current=true]:opacity-100 aria-[current=true]:outline-primary",
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
              Loading photo…
            </p>
          ) : (
            <section className="flex flex-1 flex-col items-center justify-center px-6 text-center">
              <h2 className="font-heading text-[27px]/[1.2]">
                {retry ? "Could not load photo" : "Photo not available"}
              </h2>
              <p className="mt-3 max-w-100 text-sm text-muted" role="alert">
                {retry
                  ? "Please try again."
                  : personName
                    ? `This photo is not available to ${personName}.`
                    : "This photo is not available to you."}
              </p>
              <div className="mt-6 flex gap-3">
                {retry && (
                  <Button onClick={retry} variant="outline">
                    Try again
                  </Button>
                )}
                <Button onClick={close} variant="outline">
                  Back to album
                </Button>
              </div>
            </section>
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
