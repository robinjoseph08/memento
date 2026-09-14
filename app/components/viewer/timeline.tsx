import { ChevronDown, ChevronUp } from "lucide-react";
import { useEffect, useRef, useState, type RefObject } from "react";

import { useMediaQuery } from "../../hooks/use-media-query";
import { cn } from "../../lib/utils";
import { monthLabel } from "./labels";

type Month = { key: string; top: number };

// lastMonth is the latest month starting at or above a document position.
function lastMonth(months: Month[], top: number) {
  for (let index = months.length - 1; index >= 0; index -= 1)
    if (months[index].top <= top) return months[index];
  return undefined;
}

const emptyLayout = { months: [] as Month[], height: 0, viewport: 0, rail: 0 };

// The timeline stands in for the scrollbar beside a long gallery, like Immich.
// On wide screens it maps the whole document onto a rail at the right edge: a
// dot for each month with a capture day, a year label where each year begins,
// a marker at the viewport's position, and the month under the pointer.
// Clicking or dragging scrolls there and arrow keys step between months. On
// phones there is no rail; a grab handle appears at the scroll position while
// the page is scrolling and dragging it scrubs with the month beside it. Month
// positions come from the day sections inside `sectionsRef`, so nothing beyond
// the rendered gallery is needed.
export function Timeline({
  sectionsRef,
}: {
  sectionsRef: RefObject<HTMLElement | null>;
}) {
  // Below the album header's desktop breakpoint the rail gives way to the
  // handle.
  const compact = useMediaQuery("(max-width: 760px)");
  const railRef = useRef<HTMLDivElement>(null);
  // Where the finger took hold of the phone handle, as a fraction of the rail
  // above the handle's centre, so grabbing does not move the page.
  const grabRef = useRef(0);
  const [layout, setLayout] = useState(emptyLayout);
  const [scrollTop, setScrollTop] = useState(() => window.scrollY);
  const [scrolling, setScrolling] = useState(false);
  const [pointer, setPointer] = useState<number | null>(null);
  const [dragging, setDragging] = useState(false);
  const { months, height } = layout;
  const visible = months.length > 0 && height > layout.viewport + 1;
  // The phone handle behaves like a scrollbar thumb and reaches the bottom of
  // the rail at the end of the page; the desktop rail spans the document so
  // its month dots sit where their sections are.
  const range = compact ? Math.max(1, height - layout.viewport) : height;

  useEffect(() => {
    const measure = () => {
      const tops = new Map<string, number>();
      for (const section of sectionsRef.current?.querySelectorAll<HTMLElement>(
        "[data-date]",
      ) ?? []) {
        const key = section.dataset.date?.slice(0, 7) ?? "";
        if (!tops.has(key))
          tops.set(key, section.getBoundingClientRect().top + window.scrollY);
      }
      setLayout({
        months: [...tops].map(([key, top]) => ({ key, top })),
        height: document.documentElement.scrollHeight,
        viewport: window.innerHeight,
        rail: railRef.current?.getBoundingClientRect().height ?? 0,
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(document.documentElement);
    if (sectionsRef.current) observer.observe(sectionsRef.current);
    window.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [sectionsRef]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onScroll = () => {
      setScrollTop(window.scrollY);
      setScrolling(true);
      clearTimeout(timer);
      timer = setTimeout(() => setScrolling(false), 1500);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      clearTimeout(timer);
      window.removeEventListener("scroll", onScroll);
    };
  }, []);

  // The native scrollbar would sit on top of the rail, so it hides while the
  // timeline is the way to scrub.
  useEffect(() => {
    if (!visible) return;
    const style = document.documentElement.style;
    const previous = style.scrollbarWidth;
    style.scrollbarWidth = "none";
    return () => {
      style.scrollbarWidth = previous;
    };
  }, [visible]);

  // Positions are fractions of the range so the rail can be any height.
  const percent = (top: number) => `${(top / range) * 100}%`;
  const pixels = (top: number) => (top / height) * layout.rail;
  const monthAt = (top: number) => lastMonth(months, top) ?? months[0];
  const fraction = (clientY: number) => {
    const rect = railRef.current?.getBoundingClientRect();
    if (!rect?.height) return 0;
    return Math.min(1, Math.max(0, (clientY - rect.top) / rect.height));
  };
  const scrollTo = (top: number) => window.scrollTo({ top });

  // Dots and year labels that would overlap a placed one are skipped.
  const dots: Month[] = [];
  const years: Month[] = [];
  months.forEach((month, index) => {
    const dot = dots.at(-1);
    if (!dot || pixels(month.top) - pixels(dot.top) >= 4) dots.push(month);
    const year = years.at(-1);
    const starts = month.key.slice(0, 4) !== months[index - 1]?.key.slice(0, 4);
    if (starts && (!year || pixels(month.top) - pixels(year.top) >= 20))
      years.push(month);
  });
  const current = visible ? monthAt(scrollTop) : undefined;
  const shown = visible && (!compact || scrolling || dragging);
  const label = compact
    ? current && monthLabel(current.key)
    : pointer !== null && monthLabel(monthAt(pointer * range).key);

  return (
    <div
      aria-label="Timeline"
      aria-orientation="vertical"
      aria-valuemax={Math.max(0, months.length - 1)}
      aria-valuemin={0}
      aria-valuenow={current ? months.indexOf(current) : 0}
      aria-valuetext={current ? monthLabel(current.key) : undefined}
      className={cn(
        "fixed top-24 right-0 bottom-8 z-40 w-12 cursor-pointer touch-none select-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
        !visible && "invisible",
        compact &&
          "pointer-events-none transition-opacity duration-200 motion-reduce:transition-none",
        compact && !shown && "opacity-0 focus-visible:opacity-100",
        compact && dragging && "pointer-events-auto",
      )}
      onKeyDown={(event) => {
        const target =
          event.key === "ArrowDown" || event.key === "PageDown"
            ? months.find((month) => month.top > scrollTop + 1)
            : event.key === "ArrowUp" || event.key === "PageUp"
              ? lastMonth(months, scrollTop - 1)
              : event.key === "Home"
                ? months[0]
                : event.key === "End"
                  ? months.at(-1)
                  : undefined;
        if (!target) return;
        event.preventDefault();
        scrollTo(target.top);
      }}
      onPointerDown={(event) => {
        event.currentTarget.setPointerCapture(event.pointerId);
        setDragging(true);
        const at = fraction(event.clientY);
        setPointer(at);
        grabRef.current = compact ? at - scrollTop / range : 0;
        if (!compact) scrollTo(at * range);
      }}
      onPointerLeave={() => {
        if (!dragging) setPointer(null);
      }}
      onPointerMove={(event) => {
        const at = fraction(event.clientY);
        setPointer(at);
        if (dragging)
          scrollTo(Math.min(1, Math.max(0, at - grabRef.current)) * range);
      }}
      onPointerUp={(event) => {
        setDragging(false);
        if (event.pointerType !== "mouse") setPointer(null);
      }}
      ref={railRef}
      role="slider"
      tabIndex={visible ? 0 : -1}
    >
      {compact ? (
        <>
          {dragging && label && (
            <span
              aria-hidden="true"
              className="absolute right-11 -translate-y-1/2 rounded-full bg-accent px-3 py-1.5 text-sm whitespace-nowrap shadow-md"
              style={{ top: percent(scrollTop) }}
            >
              {label}
            </span>
          )}
          {visible && (
            <span
              aria-hidden="true"
              className={cn(
                "absolute right-1 flex h-12 w-7 -translate-y-1/2 touch-none flex-col items-center justify-center rounded-full bg-accent text-foreground shadow-md",
                shown ? "pointer-events-auto" : "pointer-events-none",
              )}
              style={{ top: percent(scrollTop) }}
            >
              <ChevronUp className="size-4" strokeWidth={2.5} />
              <ChevronDown className="size-4" strokeWidth={2.5} />
            </span>
          )}
        </>
      ) : (
        <>
          {dots.map((month) => (
            <span
              aria-hidden="true"
              className="absolute right-[7px] size-[3px] -translate-y-1/2 rounded-full bg-muted"
              key={month.key}
              style={{ top: percent(month.top) }}
            />
          ))}
          {years.map((month) => (
            <span
              aria-hidden="true"
              className="absolute right-3.5 -translate-y-1/2 rounded-sm bg-background/80 px-1 text-[11px] leading-4 text-muted"
              key={month.key}
              style={{ top: percent(month.top) }}
            >
              {month.key.slice(0, 4)}
            </span>
          ))}
          {visible && (
            <span
              aria-hidden="true"
              className="absolute right-1 h-0.5 w-3.5 -translate-y-1/2 rounded-full bg-primary"
              style={{ top: percent(scrollTop) }}
            />
          )}
          {visible && pointer !== null && (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute right-0 flex -translate-y-1/2 items-center"
              style={{ top: `${pointer * 100}%` }}
            >
              <span className="rounded-sm border border-border bg-surface px-2 py-0.5 text-xs whitespace-nowrap">
                {label}
              </span>
              <span className="h-px w-3 bg-primary" />
            </span>
          )}
        </>
      )}
    </div>
  );
}
