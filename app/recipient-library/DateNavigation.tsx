import { useRef, useState } from "react";

import type { MediaChronologyDate } from "../types/generated/library";

const UNDATED_VALUE = "undated";

function captureDateValue(captureDate: string | null) {
  return captureDate ?? UNDATED_VALUE;
}

function captureDateLabel(captureDate: string | null) {
  if (captureDate === null) return "Date unavailable";
  const parsed = new Date(`${captureDate}T00:00:00Z`);
  if (Number.isNaN(parsed.valueOf())) return captureDate;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "long",
    timeZone: "UTC",
  }).format(parsed);
}

function countLabel(count: number) {
  return `${count} ${count === 1 ? "photo" : "photos"}`;
}

function dateDescription(date: MediaChronologyDate) {
  return `${captureDateLabel(date.capture_date)}, ${countLabel(date.media_count)}`;
}

export function DateNavigation({
  activeDate,
  busy,
  dates,
  onSelect,
}: {
  activeDate: string | null | undefined;
  busy: boolean;
  dates: MediaChronologyDate[];
  onSelect: (captureDate: string | null, replace?: boolean) => void;
}) {
  const selectedIndex = dates.findIndex(
    (date) => date.capture_date === activeDate,
  );
  const activeIndex = selectedIndex >= 0 ? selectedIndex : 0;
  const active = dates[activeIndex];

  if (!active) return null;

  return (
    <>
      <label className="mobile-date-nav">
        <span>Jump to date</span>
        <select
          aria-busy={busy}
          aria-label="Jump to date"
          onChange={(event) => {
            const selected = dates[event.target.selectedIndex];
            if (selected) onSelect(selected.capture_date);
          }}
          value={captureDateValue(active.capture_date)}
        >
          {dates.map((date) => (
            <option
              key={captureDateValue(date.capture_date)}
              value={captureDateValue(date.capture_date)}
            >
              {captureDateLabel(date.capture_date)} (
              {countLabel(date.media_count)})
            </option>
          ))}
        </select>
        {busy ? (
          <span aria-live="polite" className="date-navigation-status">
            Loading selected date…
          </span>
        ) : null}
      </label>
      <DateRail
        activeIndex={activeIndex}
        busy={busy}
        dates={dates}
        onSelect={onSelect}
      />
    </>
  );
}

function DateRail({
  activeIndex,
  busy,
  dates,
  onSelect,
}: {
  activeIndex: number;
  busy: boolean;
  dates: MediaChronologyDate[];
  onSelect: (captureDate: string | null, replace?: boolean) => void;
}) {
  const rail = useRef<HTMLDivElement>(null);
  const currentScrubbedIndex = useRef<number | undefined>(undefined);
  const [hoveredIndex, setHoveredIndex] = useState<number>();

  function indexAt(clientY: number) {
    const bounds = rail.current?.getBoundingClientRect();
    if (!bounds || bounds.height === 0) return 0;
    const progress = Math.max(
      0,
      Math.min(1, (clientY - bounds.top) / bounds.height),
    );
    return Math.min(dates.length - 1, Math.floor(progress * dates.length));
  }

  function selectIndex(index: number) {
    const selected = dates[index];
    if (selected) onSelect(selected.capture_date);
  }

  function keyboardIndex(key: string) {
    const pageSize = Math.max(1, Math.floor(dates.length / 10));
    switch (key) {
      case "ArrowUp":
      case "ArrowLeft":
        return Math.max(0, activeIndex - 1);
      case "ArrowDown":
      case "ArrowRight":
        return Math.min(dates.length - 1, activeIndex + 1);
      case "PageUp":
        return Math.max(0, activeIndex - pageSize);
      case "PageDown":
        return Math.min(dates.length - 1, activeIndex + pageSize);
      case "Home":
        return 0;
      case "End":
        return dates.length - 1;
      default:
        return undefined;
    }
  }

  const hovered = hoveredIndex === undefined ? undefined : dates[hoveredIndex];
  const active = dates[activeIndex];
  const markerPosition =
    dates.length === 1 ? 0 : (activeIndex / (dates.length - 1)) * 100;
  const tickIndexes = dates
    .map((date, index) => ({ date, index }))
    .filter(({ date, index }) => {
      if (index === 0 || index === dates.length - 1) return true;
      return (
        date.capture_date?.slice(0, 4) !==
        dates[index - 1]?.capture_date?.slice(0, 4)
      );
    });

  return (
    <nav aria-label="Photo date navigation" className="date-rail">
      <div
        aria-busy={busy}
        aria-label="Photo dates"
        aria-orientation="vertical"
        aria-valuemax={dates.length - 1}
        aria-valuemin={0}
        aria-valuenow={activeIndex}
        aria-valuetext={dateDescription(active)}
        className="date-rail-slider"
        onKeyDown={(event) => {
          const index = keyboardIndex(event.key);
          if (index === undefined) return;
          event.preventDefault();
          selectIndex(index);
        }}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.preventDefault();
          const index = indexAt(event.clientY);
          currentScrubbedIndex.current = index;
          setHoveredIndex(index);
          try {
            event.currentTarget.setPointerCapture(event.pointerId);
          } catch {
            // Synthetic and interrupted pointers still commit through the rail events.
          }
        }}
        onPointerLeave={(event) => {
          if (!event.currentTarget.hasPointerCapture(event.pointerId)) {
            setHoveredIndex(undefined);
          }
        }}
        onPointerMove={(event) => {
          const index = indexAt(event.clientY);
          setHoveredIndex(index);
          if (currentScrubbedIndex.current !== undefined) {
            currentScrubbedIndex.current = index;
          }
        }}
        onPointerUp={(event) => {
          if (event.currentTarget.hasPointerCapture(event.pointerId)) {
            try {
              event.currentTarget.releasePointerCapture(event.pointerId);
            } catch {
              // The pointer may already have been released by the browser.
            }
          }
          const finalIndex = currentScrubbedIndex.current;
          currentScrubbedIndex.current = undefined;
          if (finalIndex !== undefined && finalIndex !== activeIndex) {
            selectIndex(finalIndex);
          }
        }}
        onPointerCancel={() => {
          currentScrubbedIndex.current = undefined;
        }}
        ref={rail}
        role="slider"
        tabIndex={0}
      >
        <span aria-hidden="true" className="date-rail-track">
          {tickIndexes.map(({ date, index }) => (
            <span
              className="date-rail-tick"
              data-active={index === activeIndex || undefined}
              key={captureDateValue(date.capture_date)}
              style={{
                top: `${(index / Math.max(1, dates.length - 1)) * 100}%`,
              }}
            />
          ))}
          <span
            className="date-rail-marker"
            style={{ top: `${markerPosition}%` }}
          />
        </span>
      </div>
      <output className="date-rail-preview">
        {hovered ? (
          <>
            {captureDateLabel(hovered.capture_date)} ·{" "}
            {countLabel(hovered.media_count)}
          </>
        ) : (
          <>
            {captureDateLabel(active.capture_date)} ·{" "}
            {countLabel(active.media_count)}
          </>
        )}
      </output>
    </nav>
  );
}
