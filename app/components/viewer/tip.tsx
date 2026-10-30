import * as PopoverPrimitive from "@radix-ui/react-popover";
import {
  useId,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { Button } from "../ui/button";

// Room left around the target inside the lit area.
const PAD = 6;
// Height the callout needs below its target, with the longest Tip wrapping
// on a narrow phone.
const CALLOUT_ROOM = 240;
// How long the page has to itself before a Tip arrives, so a viewer sees
// where they landed before anything dims.
const ARRIVAL_MS = 600;
// The longest a glide to the target is waited out, for browsers that do not
// say when scrolling ends.
const GLIDE_MS = 700;

// Tip dims everything but its target and points at it with a callout until
// the viewer presses Got it, presses Escape, taps the dimmed page, or reaches
// for the target itself; each of those calls onDone. It arrives after a
// moment, gliding the page first when the callout would not fit below the
// target, and fades in. While it shows, the target stays where the viewer
// sees it as the page changes around it, and comes back into view when the
// window resizes or the page scrolls itself as focus moves, as closing the
// lightbox does. Inside the lightbox it renders into the dialog, which keeps
// taps and focus, and leaves the page behind it alone.
export function Tip({
  target,
  title,
  children,
  onDone,
}: {
  target: HTMLElement;
  title: string;
  children: ReactNode;
  onDone: () => void;
}) {
  const titleId = useId();
  const [box, setBox] = useState(() => target.getBoundingClientRect());
  const [shown, setShown] = useState(false);
  useLayoutEffect(() => {
    const scrolls = !target.closest("[role=dialog]");
    let showing = false;
    // The target's top within the page, which scrolling does not change.
    let placed = 0;
    const pageTop = () => target.getBoundingClientRect().top + window.scrollY;
    const measure = () => {
      const next = target.getBoundingClientRect();
      setBox((current) =>
        current.top === next.top &&
        current.left === next.left &&
        current.width === next.width &&
        current.height === next.height
          ? current
          : next,
      );
    };
    // How far the page must move for the callout to fit below the target.
    const shortfall = () => {
      const now = target.getBoundingClientRect();
      return now.top < 0 || now.bottom + CALLOUT_ROOM > window.innerHeight
        ? now.top - PAD * 3
        : 0;
    };
    const settle = () => {
      const distance = showing && scrolls ? shortfall() : 0;
      if (distance) window.scrollBy({ top: distance, behavior: "instant" });
      placed = pageTop();
      measure();
    };
    const follow = () => {
      const moved = pageTop() - placed;
      if (showing && scrolls && Math.abs(moved) >= 1)
        window.scrollBy({ top: moved, behavior: "instant" });
      placed = pageTop();
      measure();
    };
    const reveal = () => {
      if (showing) return;
      showing = true;
      settle();
      setShown(true);
    };
    let glide = 0;
    const arrival = window.setTimeout(() => {
      const page = document.scrollingElement ?? document.documentElement;
      const end = Math.max(
        0,
        Math.min(
          window.scrollY + shortfall(),
          page.scrollHeight - window.innerHeight,
        ),
      );
      if (!scrolls || Math.abs(end - window.scrollY) < 1) {
        reveal();
        return;
      }
      const still = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      window.addEventListener("scrollend", reveal, { once: true });
      glide = window.setTimeout(reveal, GLIDE_MS);
      window.scrollTo({ top: end, behavior: still ? "instant" : "smooth" });
    }, ARRIVAL_MS);
    const resizes = new ResizeObserver(follow);
    resizes.observe(target);
    resizes.observe(document.body);
    const mutations = new MutationObserver(follow);
    mutations.observe(document.body, { childList: true, subtree: true });
    let frame = 0;
    const afterFocus = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(settle);
    };
    window.addEventListener("resize", settle);
    window.addEventListener("focusin", afterFocus);
    window.addEventListener("scroll", measure, {
      capture: true,
      passive: true,
    });
    return () => {
      window.clearTimeout(arrival);
      window.clearTimeout(glide);
      cancelAnimationFrame(frame);
      resizes.disconnect();
      mutations.disconnect();
      window.removeEventListener("scrollend", reveal);
      window.removeEventListener("resize", settle);
      window.removeEventListener("focusin", afterFocus);
      window.removeEventListener("scroll", measure, { capture: true });
    };
  }, [target]);
  const anchor = useMemo(() => ({ current: target }), [target]);
  const container = target.closest<HTMLElement>("[role=dialog]") ?? undefined;
  const top = Math.max(0, box.top - PAD);
  const bottom = box.bottom + PAD;
  const left = Math.max(0, box.left - PAD);
  const right = box.right + PAD;
  const shade =
    "pointer-events-auto fixed z-60 bg-black/70 animate-in duration-300 fade-in-0 motion-reduce:animate-none";
  if (!shown) return null;
  return (
    <PopoverPrimitive.Root
      onOpenChange={(open) => {
        if (!open) onDone();
      }}
      open
    >
      <PopoverPrimitive.Anchor virtualRef={anchor} />
      <PopoverPrimitive.Portal container={container}>
        <div aria-hidden="true">
          <div
            className={shade}
            style={{ top: 0, left: 0, right: 0, height: top }}
          />
          <div
            className={shade}
            style={{ top: bottom, left: 0, right: 0, bottom: 0 }}
          />
          <div
            className={shade}
            style={{ top, left: 0, width: left, height: bottom - top }}
          />
          <div
            className={shade}
            style={{ top, left: right, right: 0, height: bottom - top }}
          />
          <div
            className="pointer-events-none fixed z-60 rounded-md ring-2 ring-primary"
            style={{ top, left, width: right - left, height: bottom - top }}
          />
        </div>
      </PopoverPrimitive.Portal>
      {/* A Radix portal takes a single child, so the callout has its own. */}
      <PopoverPrimitive.Portal container={container}>
        <PopoverPrimitive.Content
          aria-labelledby={titleId}
          className="z-61 w-72 max-w-[calc(100vw-24px)] animate-in rounded-lg bg-primary p-4 text-sm text-primary-foreground shadow-lg outline-hidden duration-300 fade-in-0 data-[side=bottom]:slide-in-from-top-2 data-[side=top]:slide-in-from-bottom-2 motion-reduce:animate-none"
          collisionPadding={12}
          onCloseAutoFocus={(event) => event.preventDefault()}
          // Focus moves on its own as pages and the lightbox open and close,
          // which says nothing about whether the viewer saw the Tip.
          onFocusOutside={(event) => event.preventDefault()}
          sideOffset={PAD + 8}
        >
          <h2 className="font-heading text-xl/tight" id={titleId}>
            {title}
          </h2>
          <p className="mt-2">{children}</p>
          <Button
            className="mt-4 bg-primary-foreground text-primary hover:bg-primary-foreground/90"
            onClick={onDone}
            size="sm"
          >
            Got it
          </Button>
          <PopoverPrimitive.Arrow
            aria-hidden="true"
            className="fill-primary"
            height={8}
            width={16}
          />
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}
