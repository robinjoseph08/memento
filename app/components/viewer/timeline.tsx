import { ChevronDown, ChevronUp } from "lucide-react";
import { useEffect, useRef, useState, type RefObject } from "react";

import { useMediaQuery } from "../../hooks/use-media-query";
import { cn } from "../../lib/utils";
import { dayLabel, monthLabel, shortDayLabel } from "./labels";

// A mark is one unit of the timeline: a YYYY-MM month or a YYYY-MM-DD day,
// with the document position where it begins.
type Mark = { key: string; top: number };

// lastMark is the latest mark starting at or above a document position.
function lastMark(marks: Mark[], top: number) {
  for (let index = marks.length - 1; index >= 0; index -= 1)
    if (marks[index].top <= top) return marks[index];
  return undefined;
}

// An album that spans less than about three months is scrubbed by day; the
// months would all be the same one, or nearly so, and say nothing.
const dayUnitLimit = 92 * 24 * 60 * 60 * 1000;

function unitFor(days: string[]) {
  if (days.length < 2) return "day";
  // The library lists days newest first, so the span is a distance.
  const span = Math.abs(Date.parse(days.at(-1)!) - Date.parse(days[0]));
  return span < dayUnitLimit ? "day" : "month";
}

const emptyLayout = {
  unit: "month" as "month" | "day",
  marks: [] as Mark[],
  height: 0,
  viewport: 0,
  rail: 0,
};

// The timeline stands in for the scrollbar beside a long gallery, like Immich.
// On wide screens it maps the whole document onto a rail at the right edge: a
// dot for each mark, a label where each year (or, by day, each day that fits)
// begins, a marker at the viewport's position, and the mark under the
// pointer. Clicking or dragging scrolls there and arrow keys step between
// marks. On phones there is no rail; a grab handle appears at the scroll
// position while the page is scrolling and dragging it scrubs with the mark
// beside it. Positions come from the day sections inside `sectionsRef`, so
// nothing beyond the rendered gallery is needed. A single day has nothing to
// scrub between, so the timeline stays out of the way.
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
  const { unit, marks, height } = layout;
  const visible = marks.length > 1 && height > layout.viewport + 1;
  const markLabel = unit === "day" ? dayLabel : monthLabel;
  // The phone handle behaves like a scrollbar thumb and reaches the bottom of
  // the rail at the end of the page; the desktop rail spans the document so
  // its month dots sit where their sections are.
  const range = compact ? Math.max(1, height - layout.viewport) : height;

  useEffect(() => {
    const measure = () => {
      const days = new Map<string, number>();
      for (const section of sectionsRef.current?.querySelectorAll<HTMLElement>(
        "[data-date]",
      ) ?? []) {
        const key = section.dataset.date ?? "";
        if (!days.has(key))
          days.set(key, section.getBoundingClientRect().top + window.scrollY);
      }
      const unit = unitFor([...days.keys()]);
      const tops = new Map<string, number>();
      for (const [day, top] of days) {
        const key = unit === "day" ? day : day.slice(0, 7);
        if (!tops.has(key)) tops.set(key, top);
      }
      setLayout({
        unit,
        marks: [...tops].map(([key, top]) => ({ key, top })),
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
  const markAt = (top: number) => lastMark(marks, top) ?? marks[0];
  const fraction = (clientY: number) => {
    const rect = railRef.current?.getBoundingClientRect();
    if (!rect?.height) return 0;
    return Math.min(1, Math.max(0, (clientY - rect.top) / rect.height));
  };
  const scrollTo = (top: number) => window.scrollTo({ top });

  // Dots and labels that would overlap a placed one are skipped. By month the
  // labels are the years; by day, every day that fits, with the year under
  // the first day and wherever the year changes so a span across New Year
  // reads right.
  const dots: Mark[] = [];
  const labels: { mark: Mark; year: boolean }[] = [];
  marks.forEach((mark, index) => {
    const dot = dots.at(-1);
    if (!dot || pixels(mark.top) - pixels(dot.top) >= 4) dots.push(mark);
    const last = labels.at(-1);
    const newYear = mark.key.slice(0, 4) !== marks[index - 1]?.key.slice(0, 4);
    const year = unit === "day" && newYear;
    // A label with the year under it needs room below before the next one.
    const room = last?.year ? 34 : 20;
    if (
      (unit === "day" || newYear) &&
      (!last || pixels(mark.top) - pixels(last.mark.top) >= room)
    )
      labels.push({ mark, year });
  });
  const current = visible ? markAt(scrollTop) : undefined;
  const shown = visible && (!compact || scrolling || dragging);
  const label = compact
    ? current && markLabel(current.key)
    : pointer !== null && markLabel(markAt(pointer * range).key);

  return (
    <div
      aria-label="Timeline"
      aria-orientation="vertical"
      aria-valuemax={Math.max(0, marks.length - 1)}
      aria-valuemin={0}
      aria-valuenow={current ? marks.indexOf(current) : 0}
      aria-valuetext={current ? markLabel(current.key) : undefined}
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
            ? marks.find((mark) => mark.top > scrollTop + 1)
            : event.key === "ArrowUp" || event.key === "PageUp"
              ? lastMark(marks, scrollTop - 1)
              : event.key === "Home"
                ? marks[0]
                : event.key === "End"
                  ? marks.at(-1)
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
          {dots.map((mark) => (
            <span
              aria-hidden="true"
              className="absolute right-[7px] size-[3px] -translate-y-1/2 rounded-full bg-muted"
              key={mark.key}
              style={{ top: percent(mark.top) }}
            />
          ))}
          {labels.map(({ mark, year }) => (
            <span
              aria-hidden="true"
              className="absolute right-3.5 -translate-y-1/2 rounded-sm bg-background/80 px-1 text-[11px] leading-4 whitespace-nowrap text-muted"
              key={mark.key}
              style={{ top: percent(mark.top) }}
            >
              {year && (
                <span className="absolute top-full right-1 text-[10px] leading-3">
                  {mark.key.slice(0, 4)}
                </span>
              )}
              {unit === "day" ? shortDayLabel(mark.key) : mark.key.slice(0, 4)}
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
