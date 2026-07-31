import { useEffect, useRef, useState } from "react";

import { preferredScrollBehavior } from "../motion";

export function DateNavigation({ dates }: { dates: string[] }) {
  return (
    <>
      <label className="mobile-date-nav">
        Jump to date
        <select
          onChange={(change) =>
            document
              .getElementById(`date-${change.target.selectedIndex}`)
              ?.scrollIntoView({ behavior: preferredScrollBehavior() })
          }
        >
          {dates.map((date) => (
            <option key={date}>{date}</option>
          ))}
        </select>
      </label>
      <DateRail dates={dates} />
    </>
  );
}

function DateRail({ dates }: { dates: string[] }) {
  const rail = useRef<HTMLElement>(null);
  const [activeIndex, setActiveIndex] = useState(0);

  useEffect(() => {
    const sections = dates
      .map((_, index) => document.getElementById(`date-${index}`))
      .filter((section): section is HTMLElement => section !== null);
    if (sections.length === 0 || !("IntersectionObserver" in window)) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort(
            (left, right) =>
              Math.abs(left.boundingClientRect.top) -
              Math.abs(right.boundingClientRect.top),
          )[0];
        if (!visible) return;
        const index = sections.indexOf(visible.target as HTMLElement);
        if (index >= 0) setActiveIndex(index);
      },
      { rootMargin: "-10% 0px -70%", threshold: 0 },
    );
    sections.forEach((section) => observer.observe(section));
    return () => observer.disconnect();
  }, [dates]);

  function jumpTo(index: number, behavior = preferredScrollBehavior()) {
    setActiveIndex(index);
    document.getElementById(`date-${index}`)?.scrollIntoView({ behavior });
  }

  function indexAt(clientY: number) {
    const bounds = rail.current?.getBoundingClientRect();
    if (!bounds || bounds.height === 0) return 0;
    const progress = Math.max(
      0,
      Math.min(0.999, (clientY - bounds.top) / bounds.height),
    );
    return Math.floor(progress * dates.length);
  }

  return (
    <nav
      aria-label="Photo dates"
      className="date-rail"
      onPointerDown={(event) => {
        if (event.button !== 0) return;
        event.currentTarget.setPointerCapture(event.pointerId);
        jumpTo(indexAt(event.clientY));
      }}
      onPointerMove={(event) => {
        if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
        jumpTo(indexAt(event.clientY), "auto");
      }}
      ref={rail}
    >
      {dates.map((date, index) => (
        <a
          aria-current={activeIndex === index ? "date" : undefined}
          href={`#date-${index}`}
          key={date}
          onClick={(event) => {
            event.preventDefault();
            jumpTo(index);
          }}
        >
          {date}
        </a>
      ))}
    </nav>
  );
}
