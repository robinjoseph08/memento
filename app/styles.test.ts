// @vitest-environment node

import { readFile } from "node:fs/promises";
import { describe, expect, test } from "vitest";

describe("responsive Visibility controls", () => {
  test("switches from the desktop matrix to the mobile membership list", async () => {
    const styles = await readFile(
      new URL("./styles.css", import.meta.url),
      "utf8",
    );
    expect(styles).toMatch(/\.visibility-mobile-list\s*\{\s*display:\s*none;/);
    const mobileRules = styles.slice(
      styles.indexOf("@media (max-width: 54rem)"),
    );
    expect(mobileRules).toMatch(
      /\.visibility-matrix-wrap\s*\{\s*display:\s*none;/,
    );
    expect(mobileRules).toMatch(
      /\.visibility-mobile-list\s*\{\s*display:\s*grid;/,
    );
  });
});
