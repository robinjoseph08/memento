import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { App } from "./App";

function renderApp() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

test("reports that the process is reachable when liveness succeeds", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ status: "live" }), { status: 200 }),
      ),
  );

  renderApp();

  expect(screen.getByRole("heading", { name: "Memento" })).toBeInTheDocument();
  expect(await screen.findByText("Process reachable")).toBeInTheDocument();
});

test("does not expose dependency detail when liveness fails", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response(null, { status: 503 })),
  );

  renderApp();

  expect(await screen.findByText("Unavailable")).toBeInTheDocument();
  expect(screen.queryByText(/database|Immich/i)).not.toBeInTheDocument();
});
