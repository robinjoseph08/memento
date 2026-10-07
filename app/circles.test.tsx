import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";

import { App } from "./App";

const curator = {
  id: "curator",
  display_name: "Robin",
  is_curator: true,
  onboarding_completed_at: "2026-01-01T00:00:00Z",
};
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("warns which Albums deleting a Circle withdraws", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          sign_in_methods: ["fake"],
        });
      if (path === "/api/circles")
        return Response.json([
          { id: "extended", name: "Extended family", members: [] },
          { id: "neighbors", name: "Neighbors", members: [] },
        ]);
      if (path === "/api/curator/circles/offers")
        return Response.json([
          {
            circle_id: "extended",
            albums: ["Coast", "Reunion", "Wedding", "Zoo", "Graduation"].map(
              (title) => ({ id: title, title }),
            ),
          },
        ]);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/circles");
  const user = userEvent.setup();
  render(<App />);
  await user.click(
    await screen.findByRole("button", { name: "Delete Extended family" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Delete Extended family?",
  });
  expect(dialog).toHaveTextContent(
    "Deleting it withdraws its Offers of “Coast”, “Reunion”, “Wedding”, and 2 more albums, so its members can't browse them through this Circle. The people in it stay in Memento.",
  );
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await user.click(screen.getByRole("button", { name: "Delete Neighbors" }));
  expect(
    await screen.findByRole("dialog", { name: "Delete Neighbors?" }),
  ).toHaveTextContent(
    "The people in it stay in Memento. They're only taken out of this Circle.",
  );
});

it("promises nothing about Offers when they could not be loaded", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.endsWith("/status"))
        return Response.json({
          claimed: true,
          person: curator,
          sign_in_methods: ["fake"],
        });
      if (path === "/api/circles")
        return Response.json([
          { id: "extended", name: "Extended family", members: [] },
        ]);
      if (path === "/api/curator/circles/offers")
        return Response.json(
          { error: { code: "internal", message: "Server exploded" } },
          { status: 500 },
        );
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  window.history.replaceState(null, "", "/curator/circles");
  const user = userEvent.setup();
  render(<App />);
  const remove = await screen.findByRole("button", {
    name: "Delete Extended family",
  });
  await waitFor(() => expect(remove).toBeEnabled());
  await user.click(remove);
  expect(
    await screen.findByRole("dialog", { name: "Delete Extended family?" }),
  ).toHaveTextContent(
    "Deleting it also withdraws anything offered to it. The people in it stay in Memento.",
  );
});
