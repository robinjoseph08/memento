export const fittedPhoto = { scale: 1, x: 0, y: 0 };

// Scale is relative to the fitted photo, not its original pixel dimensions.
// Clamp each axis independently so a portrait photo stays centered until it
// actually fills the viewport's width.
export function boundPhoto(
  view: typeof fittedPhoto,
  viewport: { width: number; height: number },
  image: { width: number; height: number },
) {
  const scale = Math.min(4, Math.max(1, view.scale));
  const fit = Math.min(
    viewport.width / image.width,
    viewport.height / image.height,
  );
  const maxX = Math.max(0, (image.width * fit * scale - viewport.width) / 2);
  const maxY = Math.max(0, (image.height * fit * scale - viewport.height) / 2);
  return {
    scale,
    x: Math.max(-maxX, Math.min(maxX, view.x)),
    y: Math.max(-maxY, Math.min(maxY, view.y)),
  };
}

// Keep the image point beneath the cursor or pinch midpoint in place.
export function zoomPhoto(
  view: typeof fittedPhoto,
  scale: number,
  point = { x: 0, y: 0 },
) {
  scale = Math.min(4, Math.max(1, scale));
  const ratio = scale / view.scale;
  return {
    scale,
    x: point.x - (point.x - view.x) * ratio,
    y: point.y - (point.y - view.y) * ratio,
  };
}
