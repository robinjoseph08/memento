import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { Pager } from "./pager";

type Props = ComponentProps<typeof Pager>;

function setup(props: Partial<Props> = {}) {
  const handlers = {
    onStep: vi.fn(),
    onTap: vi.fn(),
    onDoubleTap: vi.fn(),
    onDismiss: vi.fn(),
    onDragStart: vi.fn(),
  };
  const all: Props = {
    label: "Photo stage",
    current: { id: "photo-2", node: <p>Current</p> },
    previous: { id: "photo-1", node: <p>Previous</p> },
    next: { id: "photo-3", node: <p>Next</p> },
    locked: false,
    edges: true,
    ...handlers,
    ...props,
  };
  const result = render(<Pager {...all} />);
  const stage = screen.getByRole("group", { name: "Photo stage" });
  vi.spyOn(stage, "getBoundingClientRect").mockReturnValue({
    x: 0,
    y: 0,
    left: 0,
    top: 0,
    right: 400,
    bottom: 800,
    width: 400,
    height: 800,
    toJSON: () => ({}),
  });
  return { ...result, ...handlers, props: all, stage };
}

// A touch from one point to another with a move on the way, as a finger
// reports it.
function drag(
  stage: HTMLElement,
  from: { x: number; y: number },
  to: { x: number; y: number },
  pointerId = 1,
) {
  fireEvent.pointerDown(stage, { pointerId, clientX: from.x, clientY: from.y });
  fireEvent.pointerMove(stage, { pointerId, clientX: to.x, clientY: to.y });
  fireEvent.pointerUp(stage, { pointerId, clientX: to.x, clientY: to.y });
}

function tap(stage: HTMLElement, x: number, pointerId = 1) {
  fireEvent.pointerDown(stage, { pointerId, clientX: x, clientY: 400 });
  fireEvent.pointerUp(stage, { pointerId, clientX: x, clientY: 400 });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  // jsdom has no PointerEvent constructor with coordinates and pointer IDs.
  vi.stubGlobal(
    "PointerEvent",
    class extends MouseEvent {
      pointerId: number;
      pointerType: string;
      isPrimary: boolean;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.pointerId = init.pointerId ?? 1;
        this.pointerType = init.pointerType ?? "touch";
        this.isPrimary = init.isPrimary ?? false;
      }
    },
  );
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("turns the page as a swipe is let go, and springs back from a short one", () => {
  const { stage, onStep, onDragStart } = setup();
  drag(stage, { x: 300, y: 400 }, { x: 260, y: 405 });
  act(() => vi.runAllTimers());
  expect(onDragStart).toHaveBeenCalledOnce();
  expect(onStep).not.toHaveBeenCalled();

  // Stepping inside the touch is what lets a video start with sound.
  drag(stage, { x: 300, y: 400 }, { x: 150, y: 410 });
  expect(onStep).toHaveBeenCalledExactlyOnceWith(1);
});

it("waits for the route before another swipe, then lets one catch the settling item", () => {
  const { stage, onStep, props, rerender } = setup();
  drag(stage, { x: 100, y: 400 }, { x: 300, y: 400 });
  expect(onStep).toHaveBeenCalledExactlyOnceWith(-1);
  drag(stage, { x: 100, y: 400 }, { x: 300, y: 400 });
  expect(onStep).toHaveBeenCalledOnce();

  rerender(
    <Pager
      {...props}
      current={{ id: "photo-1", node: <p>Previous</p> }}
      next={{ id: "photo-2", node: <p>Current</p> }}
      previous={undefined}
    />,
  );
  drag(stage, { x: 300, y: 100 }, { x: 100, y: 100 });
  expect(onStep).toHaveBeenLastCalledWith(1);
  expect(onStep).toHaveBeenCalledTimes(2);
});

it("keeps a neighbour on screen as it becomes the open item", () => {
  const { props, rerender } = setup();
  const arriving = screen.getByText("Next");
  rerender(
    <Pager
      {...props}
      current={{ id: "photo-3", node: <p>Next</p> }}
      next={{ id: "photo-4", node: <p>After</p> }}
      previous={{ id: "photo-2", node: <p>Current</p> }}
    />,
  );
  // The same element, not a copy, so its image needs no loading or decoding.
  expect(screen.getByText("Next")).toBe(arriving);
  expect(arriving.parentElement).not.toHaveAttribute("aria-hidden");
});

it("judges a swipe that reports no movement on the way by where it ends", () => {
  const { stage, onStep } = setup();
  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 300, clientY: 400 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 100, clientY: 400 });
  act(() => vi.runAllTimers());
  expect(onStep).toHaveBeenCalledExactlyOnceWith(1);
});

it("resists a swipe past the last item instead of stepping", () => {
  const { stage, onStep } = setup({ next: undefined });
  drag(stage, { x: 300, y: 400 }, { x: 50, y: 400 });
  act(() => vi.runAllTimers());
  expect(onStep).not.toHaveBeenCalled();
});

it("steps from either side, reports middle taps, and tells a double tap apart", () => {
  const { stage, onStep, onTap, onDoubleTap } = setup();
  // A side tap steps once a double tap can no longer follow it.
  tap(stage, 40);
  expect(onStep).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(300));
  expect(onStep).toHaveBeenLastCalledWith(-1);
  tap(stage, 380);
  act(() => vi.advanceTimersByTime(300));
  expect(onStep).toHaveBeenLastCalledWith(1);
  expect(onTap).not.toHaveBeenCalled();

  // A double tap on a side zooms there instead of stepping.
  tap(stage, 380);
  tap(stage, 382);
  act(() => vi.advanceTimersByTime(300));
  expect(onStep).toHaveBeenCalledTimes(2);
  expect(onDoubleTap).toHaveBeenLastCalledWith({ x: 382, y: 400 });
  onDoubleTap.mockClear();

  tap(stage, 200);
  expect(onTap).toHaveBeenCalledOnce();
  tap(stage, 205);
  expect(onTap).toHaveBeenCalledOnce();
  expect(onDoubleTap).toHaveBeenCalledExactlyOnceWith({ x: 205, y: 400 });

  vi.advanceTimersByTime(300);
  tap(stage, 200);
  expect(onTap).toHaveBeenCalledTimes(2);
});

it("leaves a zoomed photo's drags to the photo and only reports its taps", () => {
  const { stage, onStep, onTap, onDragStart } = setup({ locked: true });
  drag(stage, { x: 300, y: 400 }, { x: 50, y: 400 });
  act(() => vi.runAllTimers());
  expect(onDragStart).not.toHaveBeenCalled();
  expect(onStep).not.toHaveBeenCalled();
  tap(stage, 380);
  expect(onStep).not.toHaveBeenCalled();
  expect(onTap).toHaveBeenCalledOnce();
});

it("hands a second finger to the photo's pinch without turning the page", () => {
  const { stage, onStep, onTap } = setup();
  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 300, clientY: 400 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 200, clientY: 400 });
  fireEvent.pointerDown(stage, { pointerId: 2, clientX: 350, clientY: 400 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 50, clientY: 400 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 50, clientY: 400 });
  fireEvent.pointerUp(stage, { pointerId: 2, clientX: 350, clientY: 400 });
  act(() => vi.runAllTimers());
  expect(onStep).not.toHaveBeenCalled();
  expect(onTap).not.toHaveBeenCalled();
});

it("closes on a long or quick pull down and springs back from a short one", () => {
  const { stage, onDismiss, onStep } = setup();
  drag(stage, { x: 200, y: 300 }, { x: 205, y: 360 });
  expect(onDismiss).not.toHaveBeenCalled();
  drag(stage, { x: 200, y: 300 }, { x: 205, y: 450 });
  expect(onDismiss).toHaveBeenCalledOnce();
  drag(stage, { x: 200, y: 300 }, { x: 200, y: 100 });
  expect(onDismiss).toHaveBeenCalledOnce();
  expect(onStep).not.toHaveBeenCalled();
});

it("leaves the mouse to the arrows and the photo's own double click", () => {
  const { stage, onStep, onTap, onDoubleTap } = setup();
  const mouse = { pointerType: "mouse", button: 0 };
  fireEvent.pointerDown(stage, { ...mouse, clientX: 300, clientY: 400 });
  fireEvent.pointerMove(stage, { ...mouse, clientX: 50, clientY: 400 });
  fireEvent.pointerUp(stage, { ...mouse, clientX: 50, clientY: 400 });
  for (const x of [20, 200, 200, 380]) {
    fireEvent.pointerDown(stage, { ...mouse, clientX: x, clientY: 400 });
    fireEvent.pointerUp(stage, { ...mouse, clientX: x, clientY: 400 });
  }
  act(() => vi.runAllTimers());
  expect(onStep).not.toHaveBeenCalled();
  expect(onTap).not.toHaveBeenCalled();
  expect(onDoubleTap).not.toHaveBeenCalled();
});

it("takes a waiting side tap's step as soon as another touch begins", () => {
  const { stage, onStep } = setup();
  tap(stage, 380);
  expect(onStep).not.toHaveBeenCalled();
  fireEvent.pointerDown(stage, { pointerId: 2, clientX: 200, clientY: 100 });
  expect(onStep).toHaveBeenCalledExactlyOnceWith(1);
  fireEvent.pointerUp(stage, { pointerId: 2, clientX: 200, clientY: 100 });
  act(() => vi.runAllTimers());
  expect(onStep).toHaveBeenCalledOnce();
});

it("moves keyboard focus to the item that opens, off the one that left", () => {
  const { props, rerender } = setup({
    current: { id: "photo-2", node: <div tabIndex={0}>Current</div> },
    next: { id: "photo-3", node: <div tabIndex={0}>Next</div> },
  });
  screen.getByText("Current").focus();
  rerender(
    <Pager
      {...props}
      current={{ id: "photo-3", node: <div tabIndex={0}>Next</div> }}
      next={undefined}
      previous={{ id: "photo-2", node: <div tabIndex={0}>Current</div> }}
    />,
  );
  expect(screen.getByText("Next")).toHaveFocus();
});
