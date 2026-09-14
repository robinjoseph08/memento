import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { Dialog, DialogContent, DialogTitle } from "./components/ui/dialog";

afterEach(cleanup);

it("dismisses on a primary press outside but lets a mouse back button reach the browser", async () => {
  const onOpenChange = vi.fn();
  render(
    <Dialog onOpenChange={onOpenChange} open>
      <DialogContent>
        <DialogTitle>Video details</DialogTitle>
      </DialogContent>
    </Dialog>,
  );
  expect(screen.getByRole("dialog", { name: "Video details" })).toBeVisible();
  // Radix listens for outside presses only after the opening tick.
  await new Promise((resolve) => setTimeout(resolve, 0));
  // Back (3) and forward (4) buttons are history navigation, not dismissal.
  fireEvent.pointerDown(document.body, { button: 3, pointerType: "mouse" });
  fireEvent.pointerDown(document.body, { button: 4, pointerType: "mouse" });
  expect(onOpenChange).not.toHaveBeenCalled();
  // Radix dismisses a primary press once its click lands.
  fireEvent.pointerDown(document.body, { button: 0, pointerType: "mouse" });
  fireEvent.click(document.body);
  expect(onOpenChange).toHaveBeenCalledWith(false);
});
