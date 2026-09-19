import { describe, expect, it } from "vitest";

import { disambiguatePhotoLabels, mediaLabel } from "./media-labels";

describe("mediaLabel", () => {
  it("names photos by their local capture time", () => {
    expect(
      mediaLabel({
        kind: "IMAGE",
        filename: "DSC_0123.jpg",
        captured_at: "2025-06-14T00:15:00",
      }),
    ).toBe("Photo taken June 14, 2025 at 12:15 AM");
  });

  it("numbers only photo labels that would name different actions identically", () => {
    expect(
      disambiguatePhotoLabels([
        "Photo taken June 14, 2025 at 12:15 AM",
        "Family reunion",
        "Photo taken June 14, 2025 at 12:15 AM",
      ]),
    ).toEqual([
      "Photo 1 taken June 14, 2025 at 12:15 AM",
      "Family reunion",
      "Photo 2 taken June 14, 2025 at 12:15 AM",
    ]);
  });

  it("names videos by their title with a filename fallback", () => {
    expect(
      mediaLabel({
        kind: "VIDEO",
        filename: "DSC_0123.mp4",
        title: "Family reunion",
        captured_at: "2025-06-14T00:15:00",
      }),
    ).toBe("Family reunion");
    expect(
      mediaLabel({
        kind: "VIDEO",
        filename: "DSC_0123.mp4",
        title: "",
        captured_at: "2025-06-14T00:15:00",
      }),
    ).toBe("DSC_0123");
  });
});
