import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { DateNavigation } from "./DateNavigation";

const dates = [
  { capture_date: "2026-07-27", media_count: 4, cursor: "latest" },
  { capture_date: "2022-02-03", media_count: 12, cursor: "middle" },
  { capture_date: null, media_count: 2, cursor: "undated" },
];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("jumps directly to a distant chronology bucket without focusable date links", () => {
  const onSelect = vi.fn();
  render(
    <DateNavigation
      activeDate="2026-07-27"
      busy={false}
      dates={dates}
      onSelect={onSelect}
    />,
  );

  expect(screen.queryAllByRole("link")).toHaveLength(0);
  const rail = screen.getByRole("slider", { name: "Photo dates" });
  Object.defineProperty(rail, "getBoundingClientRect", {
    value: () => ({ top: 100, height: 300 }),
  });
  Object.defineProperty(rail, "setPointerCapture", { value: vi.fn() });

  fireEvent.pointerDown(rail, { button: 0, clientY: 250, pointerId: 7 });

  expect(onSelect).toHaveBeenCalledWith("2022-02-03");
});

test("previews a hovered date and count without loading it", () => {
  const onSelect = vi.fn();
  render(
    <DateNavigation
      activeDate="2026-07-27"
      busy={false}
      dates={dates}
      onSelect={onSelect}
    />,
  );
  const rail = screen.getByRole("slider", { name: "Photo dates" });
  Object.defineProperty(rail, "getBoundingClientRect", {
    value: () => ({ top: 0, height: 300 }),
  });
  Object.defineProperty(rail, "hasPointerCapture", {
    value: () => false,
  });

  fireEvent.pointerMove(rail, { clientY: 150, pointerId: 8 });

  expect(screen.getByText("February 3, 2022 · 12 photos")).toBeVisible();
  expect(onSelect).not.toHaveBeenCalled();
});

test("drag scrubbing selects only changed chronology indexes", () => {
  const onSelect = vi.fn();
  render(
    <DateNavigation
      activeDate="2026-07-27"
      busy={false}
      dates={dates}
      onSelect={onSelect}
    />,
  );
  const rail = screen.getByRole("slider", { name: "Photo dates" });
  let captured = false;
  Object.defineProperty(rail, "getBoundingClientRect", {
    value: () => ({ top: 0, height: 300 }),
  });
  Object.defineProperty(rail, "setPointerCapture", {
    value: () => {
      captured = true;
    },
  });
  Object.defineProperty(rail, "hasPointerCapture", {
    value: () => captured,
  });
  Object.defineProperty(rail, "releasePointerCapture", {
    value: () => {
      captured = false;
    },
  });

  fireEvent.pointerDown(rail, { button: 0, clientY: 150, pointerId: 9 });
  fireEvent.pointerMove(rail, { clientY: 160, pointerId: 9 });
  fireEvent.pointerMove(rail, { clientY: 250, pointerId: 9 });
  fireEvent.pointerMove(rail, { clientY: 260, pointerId: 9 });
  fireEvent.pointerUp(rail, { clientY: 260, pointerId: 9 });

  expect(onSelect.mock.calls).toEqual([["2022-02-03"], [null, true]]);
});

test("keyboard commands traverse, page, and reach chronology endpoints", () => {
  const manyDates = Array.from({ length: 25 }, (_, index) => ({
    capture_date: `2026-01-${String(25 - index).padStart(2, "0")}`,
    media_count: index + 1,
    cursor: `cursor-${index}`,
  }));
  const onSelect = vi.fn();
  const { rerender } = render(
    <DateNavigation
      activeDate={manyDates[10].capture_date}
      busy={false}
      dates={manyDates}
      onSelect={onSelect}
    />,
  );
  const rail = screen.getByRole("slider", { name: "Photo dates" });

  fireEvent.keyDown(rail, { key: "ArrowDown" });
  expect(onSelect).toHaveBeenLastCalledWith(manyDates[11].capture_date);
  fireEvent.keyDown(rail, { key: "PageUp" });
  expect(onSelect).toHaveBeenLastCalledWith(manyDates[8].capture_date);
  fireEvent.keyDown(rail, { key: "Home" });
  expect(onSelect).toHaveBeenLastCalledWith(manyDates[0].capture_date);
  fireEvent.keyDown(rail, { key: "End" });
  expect(onSelect).toHaveBeenLastCalledWith(manyDates[24].capture_date);

  rerender(
    <DateNavigation
      activeDate={manyDates[24].capture_date}
      busy={false}
      dates={manyDates}
      onSelect={onSelect}
    />,
  );
  expect(rail).toHaveAttribute("aria-valuenow", "24");
  expect(rail).toHaveAttribute("aria-valuetext", "January 1, 2026, 25 photos");
});

test("mobile select exposes every date and its count with busy state", () => {
  const onSelect = vi.fn();
  render(
    <DateNavigation activeDate={null} busy dates={dates} onSelect={onSelect} />,
  );

  const select = screen.getByRole("combobox", { name: "Jump to date" });
  expect(select).toBeEnabled();
  expect(select).toHaveAttribute("aria-busy", "true");
  expect(screen.getAllByRole("option")).toHaveLength(3);
  expect(
    screen.getByRole("option", { name: "Date unavailable (2 photos)" }),
  ).toBeInTheDocument();

  fireEvent.change(select, { target: { value: "2022-02-03" } });
  expect(onSelect).toHaveBeenCalledWith("2022-02-03");
});
