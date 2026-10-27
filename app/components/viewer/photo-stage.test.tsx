import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, useState, type ComponentProps } from "react";
import { afterEach, expect, it, vi } from "vitest";

import { PhotoStage, type PhotoZoom } from "./photo-stage";

function PhotoWithActions(
  props: Omit<ComponentProps<typeof PhotoStage>, "actionsTarget">,
) {
  const [actionsTarget, setActionsTarget] = useState<HTMLDivElement | null>(
    null,
  );
  return (
    <>
      <div aria-label="Photo actions" ref={setActionsTarget} role="group" />
      <PhotoStage {...props} actionsTarget={actionsTarget} />
    </>
  );
}

function setup() {
  const onZoomChange = vi.fn();
  const zoom = createRef<PhotoZoom>();
  const result = render(
    <PhotoWithActions
      alt="Lake"
      onZoomChange={onZoomChange}
      ref={zoom}
      src="/lake.jpg"
    />,
  );
  const stage = screen.getByRole("group", { name: "Photo zoom" });
  const image = screen.getByRole("img", { name: "Lake" });
  vi.spyOn(stage, "getBoundingClientRect").mockReturnValue({
    x: 0,
    y: 0,
    left: 0,
    top: 0,
    right: 800,
    bottom: 600,
    width: 800,
    height: 600,
    toJSON: () => ({}),
  });
  Object.defineProperties(image, {
    naturalWidth: { value: 1600 },
    naturalHeight: { value: 1200 },
  });
  fireEvent.load(image);
  // jsdom has no PointerEvent constructor with coordinates and pointer IDs.
  vi.stubGlobal(
    "PointerEvent",
    class extends MouseEvent {
      pointerId: number;
      pointerType: string;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.pointerId = init.pointerId ?? 1;
        this.pointerType = init.pointerType ?? "touch";
      }
    },
  );
  return {
    ...result,
    stage,
    image,
    onZoomChange,
    zoom,
    transform: () => image.parentElement!.style.transform,
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("zooms with an accessible button and resets to the fitted photo", async () => {
  const user = userEvent.setup();
  const { stage, transform } = setup();
  await user.click(screen.getByRole("button", { name: "Zoom in" }));
  expect(transform()).toBe("translate(0px, 0px) scale(2)");
  expect(stage).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "Reset zoom" }));
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("zooms around a double tap, tells the lightbox, and drags the zoomed photo", () => {
  const { stage, zoom, onZoomChange, transform } = setup();
  expect(onZoomChange).toHaveBeenLastCalledWith(false);
  act(() => zoom.current!.toggleAt({ x: 500, y: 350 }));
  expect(transform()).toBe("translate(-100px, -50px) scale(2)");
  expect(onZoomChange).toHaveBeenLastCalledWith(true);
  fireEvent.pointerDown(stage, {
    pointerId: 1,
    pointerType: "mouse",
    clientX: 400,
    clientY: 300,
  });
  fireEvent.pointerMove(stage, {
    pointerId: 1,
    pointerType: "mouse",
    clientX: 480,
    clientY: 340,
  });
  fireEvent.pointerUp(stage, {
    pointerId: 1,
    pointerType: "mouse",
    clientX: 480,
    clientY: 340,
  });
  expect(transform()).toBe("translate(-20px, -10px) scale(2)");
  act(() => zoom.current!.toggleAt({ x: 0, y: 0 }));
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
  expect(onZoomChange).toHaveBeenLastCalledWith(false);
});

it("zooms on a mouse's double click but leaves a touch double tap to the pager", () => {
  const { stage, transform } = setup();
  fireEvent.doubleClick(stage, { clientX: 500, clientY: 350 });
  expect(transform()).toBe("translate(-100px, -50px) scale(2)");
  fireEvent.doubleClick(stage);
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
  // Some phones report a double tap as a double click too.
  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 400, clientY: 300 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 400, clientY: 300 });
  fireEvent.doubleClick(stage, { clientX: 500, clientY: 350 });
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("consumes wheel zoom, clamps its scale, and leaves the page unzoomed", () => {
  const { stage, transform } = setup();
  const wheel = new WheelEvent("wheel", {
    deltaY: -10000,
    clientX: 400,
    clientY: 300,
    cancelable: true,
  });
  act(() => stage.dispatchEvent(wheel));
  expect(wheel.defaultPrevented).toBe(true);
  expect(stage).toHaveFocus();
  expect(transform()).toBe("translate(0px, 0px) scale(4)");
  fireEvent.wheel(stage, { deltaY: 10000, clientX: 400, clientY: 300 });
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("leaves a one-finger drag at fitted size alone and pinches around the fingers", () => {
  const { stage, transform } = setup();
  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 400, clientY: 300 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 250, clientY: 300 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 250, clientY: 300 });
  expect(transform()).toBe("translate(0px, 0px) scale(1)");

  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 300, clientY: 300 });
  fireEvent.pointerDown(stage, { pointerId: 2, clientX: 500, clientY: 300 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 200, clientY: 300 });
  fireEvent.pointerMove(stage, { pointerId: 2, clientX: 600, clientY: 300 });
  expect(transform()).toBe("translate(0px, 0px) scale(2)");
  fireEvent.pointerUp(stage, { pointerId: 2, clientX: 600, clientY: 300 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 100, clientY: 300 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 100, clientY: 300 });
  expect(transform()).toBe("translate(-100px, 0px) scale(2)");
});

it("supports keyboard zoom without taking arrow keys at any zoom level", () => {
  const { stage, transform } = setup();
  const fittedArrow = new KeyboardEvent("keydown", {
    key: "ArrowRight",
    bubbles: true,
    cancelable: true,
  });
  stage.dispatchEvent(fittedArrow);
  expect(fittedArrow.defaultPrevented).toBe(false);
  fireEvent.keyDown(stage, { key: "+" });
  expect(transform()).toContain("scale(1.5)");
  const zoomedArrow = new KeyboardEvent("keydown", {
    key: "ArrowRight",
    bubbles: true,
    cancelable: true,
  });
  act(() => stage.dispatchEvent(zoomedArrow));
  expect(zoomedArrow.defaultPrevented).toBe(false);
  expect(transform()).toBe("translate(0px, 0px) scale(1.5)");
  fireEvent.keyDown(stage, { key: "0" });
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("reclamps panning when the viewport changes size", () => {
  let resize = () => {};
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe() {}
      disconnect() {}
    },
  );
  const { stage, transform } = setup();
  fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
  fireEvent.pointerDown(stage, { pointerId: 1, clientX: 400, clientY: 300 });
  fireEvent.pointerMove(stage, { pointerId: 1, clientX: 800, clientY: 600 });
  fireEvent.pointerUp(stage, { pointerId: 1, clientX: 800, clientY: 600 });
  expect(transform()).toBe("translate(400px, 300px) scale(2)");
  vi.mocked(stage.getBoundingClientRect).mockReturnValue({
    ...stage.getBoundingClientRect(),
    width: 300,
    height: 800,
  });
  act(() => resize());
  expect(transform()).toBe("translate(150px, 0px) scale(2)");
});

it("disables zoom until a photo loads and after an image failure", () => {
  render(<PhotoWithActions alt="Lake" src="/lake.jpg" />);
  expect(screen.getByRole("button", { name: "Zoom in" })).toBeDisabled();
  fireEvent.error(screen.getByRole("img", { name: "Lake" }));
  expect(screen.getByText("Media unavailable")).toBeVisible();
  expect(screen.getByRole("button", { name: "Zoom in" })).toBeDisabled();
});
