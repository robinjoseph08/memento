import { ZoomIn, ZoomOut } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState } from "react";
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

// Mount a new stage for each photo. Gestures belong here so panning and
// pinching cannot also trigger the lightbox's next-photo swipe. The zoom
// button renders into the header without lifting per-frame pan state there.
export function PhotoStage({
  src,
  alt,
  onStep,
  actionsTarget,
}: {
  actionsTarget: HTMLElement | null;
  src: string;
  alt: string;
  onStep: (delta: number) => void;
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const imageSizeRef = useRef<{ width: number; height: number } | null>(null);
  const viewRef = useRef(fittedPhoto);
  const pointersRef = useRef(new Map<number, Point>());
  const swipeRef = useRef<Point | null>(null);
  const [view, setView] = useState(fittedPhoto);
  const [ready, setReady] = useState(false);
  const instructionsId = useId();
  const zoomed = view.scale > 1;

  const update = useCallback((next: typeof fittedPhoto) => {
    const viewport = viewportRef.current;
    const image = imageSizeRef.current;
    if (!viewport || !image) return;
    const bounded = boundPhoto(next, viewport.getBoundingClientRect(), image);
    viewRef.current = bounded;
    setView(bounded);
  }, []);

  function localPoint(point: Point) {
    const rect = viewportRef.current!.getBoundingClientRect();
    return {
      x: point.x - rect.left - rect.width / 2,
      y: point.y - rect.top - rect.height / 2,
    };
  }

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    // React's wheel listener is passive, so use a native listener to keep
    // wheel and trackpad zoom inside the photo instead of zooming the page.
    function wheel(event: WheelEvent) {
      if (!imageSizeRef.current) return;
      event.preventDefault();
      viewport!.focus({ preventScroll: true });
      swipeRef.current = null;
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
    swipeRef.current = null;
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
          if (!ready) return;
          swipeRef.current = null;
          update(
            zoomed
              ? fittedPhoto
              : zoomPhoto(
                  viewRef.current,
                  2,
                  localPoint({ x: event.clientX, y: event.clientY }),
                ),
          );
        }}
        onDragStart={(event) => event.preventDefault()}
        onKeyDown={(event) => {
          if (!ready || event.ctrlKey || event.metaKey || event.altKey) return;
          if (event.key === "+" || event.key === "=") {
            update(zoomPhoto(viewRef.current, viewRef.current.scale * 1.5));
          } else if (event.key === "-") {
            update(zoomPhoto(viewRef.current, viewRef.current.scale / 1.5));
          } else if (event.key === "0") {
            update(fittedPhoto);
          } else return;
          event.preventDefault();
          event.stopPropagation();
        }}
        onLostPointerCapture={(event) => cancelPointer(event.pointerId)}
        onPointerCancel={(event) => cancelPointer(event.pointerId)}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.stopPropagation();
          event.currentTarget.focus({ preventScroll: true });
          // Touch pointers already have implicit capture on their target.
          if (event.pointerType !== "touch")
            event.currentTarget.setPointerCapture(event.pointerId);
          const point = { x: event.clientX, y: event.clientY };
          pointersRef.current.set(event.pointerId, point);
          swipeRef.current =
            pointersRef.current.size === 1 &&
            event.pointerType === "touch" &&
            viewRef.current.scale === 1
              ? point
              : null;
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
          event.stopPropagation();
          const start = swipeRef.current;
          cancelPointer(event.pointerId);
          if (event.currentTarget.hasPointerCapture(event.pointerId))
            event.currentTarget.releasePointerCapture(event.pointerId);
          if (!start) return;
          const dx = event.clientX - start.x;
          const dy = event.clientY - start.y;
          if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy))
            onStep(dx < 0 ? 1 : -1);
        }}
        ref={viewportRef}
        role="group"
        tabIndex={0}
      >
        <div
          className="flex h-full w-full shrink-0 items-center justify-center"
          style={{
            transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})`,
          }}
        >
          <AlbumImage
            alt={alt}
            className="pointer-events-none h-full w-full bg-transparent object-contain"
            fallback="Media unavailable"
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
        Double-click, scroll, or pinch to zoom. Drag to pan. Use + and - to zoom
        and 0 to fit the photo. Left and right arrow keys change photos.
      </p>
      {src &&
        actionsTarget &&
        createPortal(
          <Button
            aria-label={zoomed ? "Reset zoom" : "Zoom in"}
            className="size-11 rounded-full p-0"
            disabled={!ready}
            onClick={() => {
              swipeRef.current = null;
              update(zoomed ? fittedPhoto : zoomPhoto(viewRef.current, 2));
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
