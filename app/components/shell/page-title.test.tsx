import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";

import { PageTitle } from "./page-title";

afterEach(() => {
  cleanup();
  document.head.innerHTML = "";
});

it("updates the server title without adding another title element", () => {
  document.head.innerHTML = "<title>Memento</title>";
  const view = render(<PageTitle title="Sign in" />);

  expect(document.title).toBe("Sign in | Memento");
  expect(document.head.querySelectorAll("title")).toHaveLength(1);

  view.rerender(<PageTitle title="Albums" />);
  expect(document.title).toBe("Albums | Memento");
  expect(document.head.querySelectorAll("title")).toHaveLength(1);
});
