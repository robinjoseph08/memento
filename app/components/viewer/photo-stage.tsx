import { ZoomIn, ZoomOut } from "lucide-react";
import {
  useCallback,
  useEffect,
  useId,
  useImperativeHandle,
  useRef,
  useState,
  type Ref,
} from "react";
import { createPortal } from "react-dom";

import { cn } from "../../lib/utils";
import { AlbumImage } from "../albums/album-image";
import { Button } from "../ui/button";
import { boundPhoto, fittedPhoto, zoomPhoto } from "./photo-zoom";

type Point = { x: number; y: number };

function midpoint(points: Point[]) {
  return {
    x: (points[0].x + points[1].x) / 2,
    y: (points[0].y + points[1].y) / 2,
  };
}
function distance(points: Point[]) {
  return Math.hypot(points[0].x - points[1].x, points[0].y - points[1].y);
}

// PhotoZoom lets the lightbox's pager zoom the photo on a double tap, since
// the pager is what tells a double tap from two taps.
export type PhotoZoom = { toggleAt: (point: Point) => void };

// One photo in the lightbox. The pager keeps a stage mounted while its photo
// moves between neighbour and open, so the image is never decoded again on
// arrival; only the active stage takes focus and offers the zoom button, and
// a stage that stops being active returns to fitted. Pinching, panning a
// zoomed photo, wheel, keyboard zoom, and a mouse's double click belong
// here; a one-finger swipe at fitted size and a double tap belong to the
// pager, which onZoomChange tells when it must stand aside. The zoom button
// renders into the header without lifting per-frame pan state there, and
// only on screens without touch, which pinch instead.
export function PhotoStage({
  src,
  alt,
  active = true,
  actionsTarget,
  onZoomChange,
  ref,
}: {
  actionsTarget: HTMLElement | null;
  src: string;
  alt: string;
  active?: boolean;
  onZoomChange?: (zoomed: boolean) => void;
  ref?: Ref<PhotoZoom>;
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const imageSizeRef = useRef<{ width: number; height: number } | null>(null);
  const viewRef = useRef(fittedPhoto);
  const pointersRef = useRef(new Map<number, Point>());
  // Some phones also report a double tap as a double click, which the pager
  // has already handled.
  const touchedRef = useRef(false);
  const [view, setView] = useState(fittedPhoto);
  // A double tap, the zoom button, and keys glide to the new zoom; pinching,
  // panning, and the wheel follow the hand exactly.
  const [gliding, setGliding] = useState(false);
  const [ready, setReady] = useState(false);
  const instructionsId = useId();
  const zoomed = view.scale > 1;
  const [wasActive, setWasActive] = useState(active);
  if (wasActive !== active) {
    setWasActive(active);
    if (!active) setView(fittedPhoto);
  }
  useEffect(() => {
    if (!active) viewRef.current = fittedPhoto;
  }, [active]);

  const update = useCallback((next: typeof fittedPhoto, glide = false) => {
    const viewport = viewportRef.current;
    const image = imageSizeRef.current;
    if (!viewport || !image) return;
    const bounded = boundPhoto(next, viewport.getBoundingClientRect(), image);
    viewRef.current = bounded;
    setView(bounded);
    setGliding(glide);
  }, []);

  function localPoint(point: Point) {
    const rect = viewportRef.current!.getBoundingClientRect();
    return {
      x: point.x - rect.left - rect.width / 2,
      y: point.y - rect.top - rect.height / 2,
    };
  }

  useImperativeHandle(ref, () => ({
    toggleAt(point) {
      if (!imageSizeRef.current) return;
      update(
        viewRef.current.scale > 1
          ? fittedPhoto
          : zoomPhoto(viewRef.current, 2, localPoint(point)),
        true,
      );
    },
  }));
  useEffect(() => {
    onZoomChange?.(zoomed);
  }, [onZoomChange, zoomed]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    // React's wheel listener is passive, so use a native listener to keep
    // wheel and trackpad zoom inside the photo instead of zooming the page.
    function wheel(event: WheelEvent) {
      if (!imageSizeRef.current) return;
      event.preventDefault();
      viewport!.focus({ preventScroll: true });
      const rect = viewport!.getBoundingClientRect();
      const unit =
        event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? rect.height : 1;
      update(
        zoomPhoto(
          viewRef.current,
          viewRef.current.scale * Math.exp(-event.deltaY * unit * 0.002),
          {
            x: event.clientX - rect.left - rect.width / 2,
            y: event.clientY - rect.top - rect.height / 2,
          },
        ),
      );
    }
    const observer = new ResizeObserver(() => update(viewRef.current));
    observer.observe(viewport);
    viewport.addEventListener("wheel", wheel, { passive: false });
    return () => {
      observer.disconnect();
      viewport.removeEventListener("wheel", wheel);
    };
  }, [update]);

  function cancelPointer(id: number) {
    pointersRef.current.delete(id);
  }

  return (
    <div className="relative h-full w-full min-w-0">
      <div
        aria-describedby={instructionsId}
        aria-label="Photo zoom"
        className={cn(
          "flex h-full w-full touch-none items-center justify-center overflow-hidden outline-none focus-visible:ring-2 focus-visible:ring-ring",
          ready &&
            (zoomed ? "cursor-grab active:cursor-grabbing" : "cursor-default"),
        )}
        onDoubleClick={(event) => {
          if (!ready || touchedRef.current) return;
          update(
            zoomed
              ? fittedPhoto
              : zoomPhoto(
                  viewRef.current,
                  2,
                  localPoint({ x: event.clientX, y: event.clientY }),
                ),
            true,
          );
        }}
        onDragStart={(event) => event.preventDefault()}
        onKeyDown={(event) => {
          if (!ready || event.ctrlKey || event.metaKey || event.altKey) return;
          if (event.key === "+" || event.key === "=") {
            update(
              zoomPhoto(viewRef.current, viewRef.current.scale * 1.5),
              true,
            );
          } else if (event.key === "-") {
            update(
              zoomPhoto(viewRef.current, viewRef.current.scale / 1.5),
              true,
            );
          } else if (event.key === "0") {
            update(fittedPhoto, true);
          } else return;
          event.preventDefault();
          event.stopPropagation();
        }}
        onLostPointerCapture={(event) => cancelPointer(event.pointerId)}
        onPointerCancel={(event) => cancelPointer(event.pointerId)}
        onPointerDown={(event) => {
          touchedRef.current = event.pointerType !== "mouse";
          if (event.button !== 0) return;
          // A click focuses the photo for its zoom keys. A touch does not,
          // since there are no keys, and phones draw the focus ring for it.
          if (event.pointerType !== "touch")
            event.currentTarget.focus({ preventScroll: true });
          // Touch pointers already have implicit capture on their target.
          if (event.pointerType !== "touch")
            event.currentTarget.setPointerCapture(event.pointerId);
          pointersRef.current.set(event.pointerId, {
            x: event.clientX,
            y: event.clientY,
          });
        }}
        onPointerMove={(event) => {
          const pointers = pointersRef.current;
          const previous = pointers.get(event.pointerId);
          if (!previous) return;
          const before = [...pointers.values()];
          const point = { x: event.clientX, y: event.clientY };
          pointers.set(event.pointerId, point);
          if (pointers.size >= 2) {
            const after = [...pointers.values()];
            const start = midpoint(before);
            const end = midpoint(after);
            const span = distance(before);
            if (!span) return;
            const next = zoomPhoto(
              viewRef.current,
              (viewRef.current.scale * distance(after)) / span,
              localPoint(start),
            );
            update({
              ...next,
              x: next.x + end.x - start.x,
              y: next.y + end.y - start.y,
            });
          } else if (viewRef.current.scale > 1) {
            update({
              ...viewRef.current,
              x: viewRef.current.x + point.x - previous.x,
              y: viewRef.current.y + point.y - previous.y,
            });
          }
        }}
        onPointerUp={(event) => {
          cancelPointer(event.pointerId);
          if (event.currentTarget.hasPointerCapture(event.pointerId))
            event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        ref={viewportRef}
        role="group"
        tabIndex={active ? 0 : -1}
      >
        <div
          className={cn(
            "flex h-full w-full shrink-0 items-center justify-center",
            gliding &&
              "transition-transform duration-250 ease-[cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:transition-none",
          )}
          style={{
            transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})`,
          }}
        >
          <AlbumImage
            alt={alt}
            className="pointer-events-none h-full w-full bg-transparent object-contain"
            fallback="Media unavailable"
            loading="eager"
            onError={() => {
              imageSizeRef.current = null;
              viewRef.current = fittedPhoto;
              setView(fittedPhoto);
              setReady(false);
            }}
            onLoad={(event) => {
              imageSizeRef.current = {
                width: event.currentTarget.naturalWidth,
                height: event.currentTarget.naturalHeight,
              };
              setReady(true);
              update(fittedPhoto);
            }}
            src={src}
          />
        </div>
      </div>
      <p className="sr-only" id={instructionsId}>
        Double-click, double-tap, scroll, or pinch to zoom. Drag to pan. Use +
        and - to zoom and 0 to fit the photo. Left and right arrow keys change
        photos.
      </p>
      {src &&
        active &&
        actionsTarget &&
        createPortal(
          <Button
            aria-label={zoomed ? "Reset zoom" : "Zoom in"}
            className="size-11 rounded-full p-0 hover:bg-white/15 pointer-coarse:hidden"
            disabled={!ready}
            onClick={() => {
              update(
                zoomed ? fittedPhoto : zoomPhoto(viewRef.current, 2),
                true,
              );
              viewportRef.current?.focus({ preventScroll: true });
            }}
            title={zoomed ? "Reset zoom" : "Zoom in"}
            variant="ghost"
          >
            {zoomed ? (
              <ZoomOut
                aria-hidden="true"
                className="size-5"
                strokeWidth={1.5}
              />
            ) : (
              <ZoomIn aria-hidden="true" className="size-5" strokeWidth={1.5} />
            )}
          </Button>,
          actionsTarget,
        )}
    </div>
  );
}
