import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, useLocation, useNavigate } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { CurationWorkspace } from "./CurationWorkspace";

vi.mock("./EventOrganizer", () => ({
  EventOrganizer: ({
    onDirtyChange,
    onSavingChange,
  }: {
    onDirtyChange?: (dirty: boolean) => void;
    onSavingChange?: (saving: boolean) => void;
  }) => (
    <section aria-label="Event organizer">
      <button onClick={() => onSavingChange?.(true)}>
        Start Event Publication
      </button>
      <button onClick={() => onSavingChange?.(false)}>
        Finish Event Publication
      </button>
      <button onClick={() => onDirtyChange?.(true)}>Edit Event</button>
    </section>
  ),
}));

vi.mock("./LooseItemOrganizer", () => ({
  LooseItemOrganizer: () => <section aria-label="Loose organizer" />,
}));

function Navigation() {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <>
      <button onClick={() => void navigate("/?workspace=drafts&loose=loose-1")}>
        Navigate to Loose
      </button>
      <output aria-label="Current search">{location.search}</output>
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function renderWorkspace() {
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/?workspace=drafts"]}>
        <Navigation />
        <CurationWorkspace
          session={{
            display_name: "Curator",
            session_type: "trusted",
            csrf_token: "c".repeat(64),
            curator: true,
            onboarding_required: false,
          }}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("cross-kind URL navigation cannot unmount an access-changing workspace", async () => {
  renderWorkspace();
  fireEvent.click(
    screen.getByRole("button", { name: "Start Event Publication" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Navigate to Loose" }));

  await waitFor(() =>
    expect(
      screen.getByRole("region", { name: "Event organizer" }),
    ).toBeVisible(),
  );
  expect(
    screen.queryByRole("region", { name: "Loose organizer" }),
  ).not.toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByLabelText("Current search")).toHaveTextContent(
      "?workspace=drafts",
    ),
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Finish Event Publication" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Navigate to Loose" }));
  await waitFor(() =>
    expect(
      screen.getByRole("region", { name: "Loose organizer" }),
    ).toBeVisible(),
  );
});

test("rejected dirty URL navigation restores the accepted workspace", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  renderWorkspace();
  fireEvent.click(screen.getByRole("button", { name: "Edit Event" }));
  fireEvent.click(screen.getByRole("button", { name: "Navigate to Loose" }));

  await waitFor(() => expect(confirm).toHaveBeenCalledOnce());
  expect(screen.getByRole("region", { name: "Event organizer" })).toBeVisible();
  await waitFor(() =>
    expect(screen.getByLabelText("Current search")).toHaveTextContent(
      "?workspace=drafts",
    ),
  );
});
