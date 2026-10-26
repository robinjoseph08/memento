import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";

// A touch this short and still is a tap.
const TAP_MS = 250;
const TAP_SLOP = 10;
// A second touch this soon after a tap lifts, in about the same place, is a
// double tap.
export const DOUBLE_TAP_MS = 200;
// A swipe past this share of the width, or flicked this fast in pixels per
// millisecond, turns the page; anything less springs back.
const PAGE_SHARE = 0.2;
const PAGE_VELOCITY = 0.5;
const PAGE_MS = 220;
// A pull down this far, or this fast, closes the lightbox.
const DISMISS_DISTANCE = 120;
const DISMISS_VELOCITY = 1.2;
// The black between neighbouring items while one is dragged past another.
const GAP = 16;
// A video's own controls run along its top and bottom edges, so touches that
// start this close to them belong to the player.
const PLAYER_CONTROLS = 64;
const EASE = "cubic-bezier(0.2, 0.8, 0.2, 1)";

// One item on the track. Its id keeps it mounted while it moves between
// neighbour and open, so what it shows is never rebuilt on arrival. A key
// instead keeps the open slide mounted from item to item.
export type Slide = { id: string; key?: string; node: ReactNode };

type Gesture = {
  id: number;
  // Where the track was when the touch landed, which is not centered when it
  // catches an item still settling from the last swipe.
  base: number;
  startX: number;
  startY: number;
  startTime: number;
  axis: "" | "x" | "y" | "none";
  lastX: number;
  lastY: number;
  lastTime: number;
  velocityX: number;
  velocityY: number;
  left: number;
  width: number;
  height: number;
};

// The lightbox's stage: the open item between its neighbours. A swipe drags
// the neighbour into view, and letting go steps to it at once, while the
// touch is still the reason, since browsers only let a video start with
// sound from inside one. The neighbour becomes the open item where it stands
// and then settles into place. A pull down shrinks the item over the fading
// black and closes past a threshold. Gestures write styles directly so a drag
// never renders the controls again.
//
// The pager owns one-finger touch gestures and taps. A mouse has the arrows
// and moves to show the controls, and a double click zooms the photo itself.
// A second finger is a pinch, which belongs to the photo, as does a drag
// while locked, so a zoomed photo only reports taps; touches on a video's own
// controls belong to the player. With edges, a tap on either outer quarter
// steps. A tap in the middle reports onTap right away. With onDoubleTap, a
// second touch soon after in the same place is a double tap instead,
// wherever it lands, so a tap on either side waits that long before
// stepping, or until another touch begins.
export function Pager({
  label,
  current,
  previous,
  next,
  locked,
  edges,
  onStep,
  onTap,
  onDoubleTap,
  onDismiss,
  onDragStart,
}: {
  label: string;
  current: Slide;
  previous?: Slide;
  next?: Slide;
  locked: boolean;
  edges: boolean;
  onStep: (delta: number) => void;
  onTap?: () => void;
  onDoubleTap?: (point: { x: number; y: number }) => void;
  onDismiss: () => void;
  onDragStart: () => void;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const itemRef = useRef<HTMLDivElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  const gestureRef = useRef<Gesture | null>(null);
  const pointersRef = useRef(new Set<number>());
  const tapRef = useRef<{ time: number; x: number; y: number } | null>(null);
  // Set from the moment a swipe commits to a page turn until the route names
  // the new item, so a second swipe cannot start from the old one. Once the
  // new item is open, a touch may catch it while it settles.
  const turningRef = useRef(false);
  // Where the track must start once the route names the new item, so the
  // item a swipe brought in keeps its place on screen as it settles.
  const arrivalRef = useRef<number | null>(null);
  const timerRef = useRef(0);
  // A side tap waiting out a possible double tap, and its direction.
  const sideTapRef = useRef({ timer: 0, delta: 0 });
  // The step a waiting side tap takes is the one current when it fires.
  const onStepRef = useRef(onStep);
  useEffect(() => {
    onStepRef.current = onStep;
  });

  const id = current.id;
  useLayoutEffect(() => {
    window.clearTimeout(timerRef.current);
    window.clearTimeout(sideTapRef.current.timer);
    sideTapRef.current.delta = 0;
    tapRef.current = null;
    // The item that was open is now an inert neighbour, which would drop
    // keyboard focus out of the dialog and take the arrow keys with it.
    const root = rootRef.current;
    const focused = document.activeElement;
    if (
      root &&
      (focused === document.body ||
        (focused &&
          root.contains(focused) &&
          !itemRef.current?.contains(focused)))
    )
      (
        itemRef.current?.querySelector<HTMLElement>('[tabindex="0"]') ??
        root.closest<HTMLElement>("[role=dialog]")
      )?.focus({ preventScroll: true });
    for (const slide of trackRef.current?.children ?? [])
      if (slide instanceof HTMLElement) place(slide, "", false);
    fade(backdropRef.current, 1, false);
    turningRef.current = false;
    const track = trackRef.current;
    const from = arrivalRef.current;
    arrivalRef.current = null;
    if (from === null || !track || reducedMotion()) {
      place(track, "", false);
      return;
    }
    place(track, `translateX(${from}px)`, false);
    // Lay out the starting place before animating away from it.
    track.getBoundingClientRect();
    place(track, "", true);
  }, [id]);
  useEffect(
    () => () => {
      window.clearTimeout(timerRef.current);
      window.clearTimeout(sideTapRef.current.timer);
    },
    [],
  );

  function pull(distance: number, height: number, animate: boolean) {
    const share = Math.min(1, distance / height);
    place(
      itemRef.current,
      distance > 0
        ? `translateY(${distance}px) scale(${1 - Math.min(share, 0.5) * 0.4})`
        : "",
      animate,
    );
    fade(backdropRef.current, 1 - Math.min(1, share * 2.2), animate);
  }

  // Let go of a gesture without acting on it, as when a pinch begins.
  function abandon() {
    const gesture = gestureRef.current;
    gestureRef.current = null;
    if (!gesture) return;
    place(trackRef.current, "", true);
    pull(0, gesture.height, true);
  }

  function decide(dx: number, dy: number) {
    if (locked) return "none";
    if (Math.abs(dx) > Math.abs(dy)) return "x";
    return dy > 0 ? "y" : "none";
  }

  function turn(delta: number, width: number, offset: number) {
    turningRef.current = true;
    arrivalRef.current = offset + delta * (width + GAP);
    // Should the route never change, the track returns rather than waiting.
    timerRef.current = window.setTimeout(() => {
      arrivalRef.current = null;
      turningRef.current = false;
      place(trackRef.current, "", true);
    }, 1000);
    onStep(delta);
  }

  function tap(gesture: Gesture, x: number, y: number) {
    tapRef.current = { time: performance.now(), x, y };
    const offset = x - gesture.left;
    const delta =
      !edges || locked
        ? 0
        : offset < gesture.width / 4
          ? -1
          : offset > (gesture.width * 3) / 4
            ? 1
            : 0;
    if (!delta) {
      onTap?.();
      return;
    }
    // A double tap zooms anywhere on the photo, so a tap on either side
    // steps only once a second one has not followed.
    if (!onDoubleTap) onStep(delta);
    else
      sideTapRef.current = {
        delta,
        timer: window.setTimeout(stepAfterSideTap, DOUBLE_TAP_MS),
      };
  }

  function stepAfterSideTap() {
    const { timer, delta } = sideTapRef.current;
    window.clearTimeout(timer);
    sideTapRef.current = { timer: 0, delta: 0 };
    if (delta) onStepRef.current(delta);
  }

  return (
    <div
      aria-label={label}
      className="absolute inset-0 touch-none overflow-hidden select-none"
      onPointerCancel={(event) => {
        pointersRef.current.delete(event.pointerId);
        if (gestureRef.current?.id === event.pointerId) abandon();
      }}
      onPointerDown={(event) => {
        // The photo's zoom button is portaled into the header, so its
        // presses reach here through React even though it sits elsewhere.
        if (
          !(event.target instanceof Node) ||
          !event.currentTarget.contains(event.target)
        )
          return;
        if (event.pointerType === "mouse") return;
        const video =
          event.target instanceof Element
            ? event.target.closest("video")
            : null;
        if (video) {
          const box = video.getBoundingClientRect();
          if (
            event.clientY < box.top + PLAYER_CONTROLS ||
            event.clientY > box.bottom - PLAYER_CONTROLS
          )
            return;
        }
        const pointers = pointersRef.current;
        if (event.isPrimary) pointers.clear();
        pointers.add(event.pointerId);
        if (pointers.size > 1) {
          abandon();
          return;
        }
        if (turningRef.current) return;
        const rect = rootRef.current!.getBoundingClientRect();
        const now = performance.now();
        // A double tap acts as the second touch lands, and the rest of that
        // touch does nothing more.
        const last = tapRef.current;
        const second =
          !!onDoubleTap &&
          !!last &&
          now - last.time < DOUBLE_TAP_MS &&
          Math.hypot(event.clientX - last.x, event.clientY - last.y) <
            TAP_SLOP * 4;
        if (second) {
          window.clearTimeout(sideTapRef.current.timer);
          sideTapRef.current = { timer: 0, delta: 0 };
          tapRef.current = null;
          onDoubleTap({ x: event.clientX, y: event.clientY });
        } else {
          // Any other touch takes a waiting side tap's step at once, so the
          // touch starts on the item it will act on.
          stepAfterSideTap();
        }
        // Hold a settling item where the touch caught it.
        const base = settlingOffset(trackRef.current);
        if (base !== 0) place(trackRef.current, `translateX(${base}px)`, false);
        gestureRef.current = {
          id: event.pointerId,
          base,
          startX: event.clientX,
          startY: event.clientY,
          startTime: now,
          axis: second ? "none" : "",
          lastX: event.clientX,
          lastY: event.clientY,
          lastTime: now,
          velocityX: 0,
          velocityY: 0,
          left: rect.left,
          width: rect.width,
          height: rect.height,
        };
      }}
      onPointerMove={(event) => {
        const gesture = gestureRef.current;
        if (!gesture || gesture.id !== event.pointerId) return;
        const dx = event.clientX - gesture.startX;
        const dy = event.clientY - gesture.startY;
        const now = performance.now();
        if (now > gesture.lastTime) {
          gesture.velocityX =
            (event.clientX - gesture.lastX) / (now - gesture.lastTime);
          gesture.velocityY =
            (event.clientY - gesture.lastY) / (now - gesture.lastTime);
        }
        gesture.lastX = event.clientX;
        gesture.lastY = event.clientY;
        gesture.lastTime = now;
        if (!gesture.axis) {
          if (Math.hypot(dx, dy) < TAP_SLOP) return;
          gesture.axis = decide(dx, dy);
          if (gesture.axis === "x" || gesture.axis === "y") onDragStart();
        }
        if (gesture.axis === "x") {
          // Past the first or last item the track resists instead of
          // revealing nothing.
          const open = dx > 0 ? previous : next;
          place(
            trackRef.current,
            `translateX(${gesture.base + (open ? dx : dx * 0.3)}px)`,
            false,
          );
        } else if (gesture.axis === "y") {
          pull(Math.max(0, dy), gesture.height, false);
        }
      }}
      onPointerUp={(event) => {
        pointersRef.current.delete(event.pointerId);
        const gesture = gestureRef.current;
        if (!gesture || gesture.id !== event.pointerId) return;
        gestureRef.current = null;
        const dx = event.clientX - gesture.startX;
        const dy = event.clientY - gesture.startY;
        const now = performance.now();
        if (!gesture.axis && Math.hypot(dx, dy) >= TAP_SLOP) {
          // A swipe that reported no movement on the way is judged by where
          // it ended and how quickly it got there.
          gesture.axis = decide(dx, dy);
          const elapsed = Math.max(1, now - gesture.startTime);
          gesture.velocityX = dx / elapsed;
          gesture.velocityY = dy / elapsed;
        }
        if (gesture.axis === "x") {
          const delta = dx < 0 ? 1 : -1;
          const flicked =
            Math.abs(gesture.velocityX) > PAGE_VELOCITY &&
            Math.sign(gesture.velocityX) === Math.sign(dx);
          if (
            (delta > 0 ? next : previous) &&
            (Math.abs(dx) > gesture.width * PAGE_SHARE || flicked)
          )
            turn(delta, gesture.width, gesture.base + dx);
          else place(trackRef.current, "", true);
          return;
        }
        // Anything else lets a caught item finish settling.
        if (gesture.base !== 0) place(trackRef.current, "", true);
        if (gesture.axis === "y") {
          if (dy > DISMISS_DISTANCE || gesture.velocityY > DISMISS_VELOCITY)
            onDismiss();
          else pull(0, gesture.height, true);
        } else if (!gesture.axis && now - gesture.startTime < TAP_MS) {
          tap(gesture, event.clientX, event.clientY);
        }
      }}
      ref={rootRef}
      role="group"
    >
      <div className="absolute inset-0 bg-black" ref={backdropRef} />
      <div className="absolute inset-0" ref={trackRef}>
        {[previous, current, next].map((slide, position) => {
          if (!slide) return null;
          const offset = position - 1;
          return (
            <div
              aria-hidden={offset !== 0 || undefined}
              className="absolute top-0 h-full w-full"
              inert={offset !== 0}
              key={slide.key ?? slide.id}
              ref={offset === 0 ? itemRef : undefined}
              style={{ left: `calc(${offset} * (100% + ${GAP}px))` }}
            >
              {slide.node}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// settlingOffset reads where an animating track is right now.
function settlingOffset(track: HTMLElement | null) {
  if (!track?.style.transition) return 0;
  const matrix = /matrix\(([^)]+)\)/.exec(getComputedStyle(track).transform);
  return matrix ? Number(matrix[1].split(",")[4]) || 0 : 0;
}

function reducedMotion() {
  return !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

function place(
  element: HTMLElement | null,
  transform: string,
  animate: boolean,
) {
  if (!element) return;
  element.style.transition =
    animate && !reducedMotion() ? `transform ${PAGE_MS}ms ${EASE}` : "";
  element.style.transform = transform;
}

function fade(element: HTMLElement | null, opacity: number, animate: boolean) {
  if (!element) return;
  element.style.transition =
    animate && !reducedMotion() ? `opacity ${PAGE_MS}ms ${EASE}` : "";
  element.style.opacity = opacity === 1 ? "" : String(opacity);
}
