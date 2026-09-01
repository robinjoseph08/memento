import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("shows the application introduction", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", {
        level: 1,
        name: "Your application starts here.",
      }),
    ).toBeInTheDocument();
  });
});
