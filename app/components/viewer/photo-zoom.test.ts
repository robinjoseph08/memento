import { expect, it } from "vitest";

import { boundPhoto, fittedPhoto, zoomPhoto } from "./photo-zoom";

it("zooms around the selected point and limits magnification", () => {
  expect(zoomPhoto(fittedPhoto, 2, { x: 100, y: -50 })).toEqual({
    scale: 2,
    x: -100,
    y: 50,
  });
  expect(zoomPhoto(fittedPhoto, 100).scale).toBe(4);
  expect(zoomPhoto(fittedPhoto, 0).scale).toBe(1);
});

it("bounds panning to the visible image rather than its letterboxed element", () => {
  const viewport = { width: 800, height: 600 };
  const portrait = { width: 400, height: 800 };
  expect(boundPhoto({ scale: 2, x: 900, y: -900 }, viewport, portrait)).toEqual(
    {
      scale: 2,
      x: 0,
      y: -300,
    },
  );
  const landscape = { width: 1600, height: 800 };
  expect(
    boundPhoto({ scale: 2, x: -900, y: 900 }, viewport, landscape),
  ).toEqual({
    scale: 2,
    x: -400,
    y: 100,
  });
});

it("recenters a fitted image after zooming out or resizing", () => {
  const fitted = boundPhoto(
    { scale: 0.5, x: 300, y: -200 },
    { width: 400, height: 800 },
    { width: 1600, height: 800 },
  );
  expect(fitted.scale).toBe(1);
  expect(Math.abs(fitted.x)).toBe(0);
  expect(Math.abs(fitted.y)).toBe(0);
});
