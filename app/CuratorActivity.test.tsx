import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { CuratorActivity } from "./CuratorActivity";

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return `${input.pathname}${input.search}`;
  return input.url;
}

function json(body: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

test("restores the activity tab and structured filters from the URL", async () => {
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      requests.push(path);
      if (path.startsWith("/api/activity/curator?")) {
        return json({ items: [], next_cursor: null });
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  render(
    <MemoryRouter
      initialEntries={[
        "/?activity_view=activity&activity_category=comment&activity_unread=true",
      ]}
    >
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <CuratorActivity
          session={{
            display_name: "Curator",
            session_type: "trusted",
            csrf_token: "csrf",
            curator: true,
            onboarding_required: false,
          }}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );

  expect(screen.getByRole("tab", { name: "Activity" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(screen.getByLabelText("Category")).toHaveValue("comment");
  expect(screen.getByLabelText("Unread only")).toBeChecked();
  await screen.findByText("No matching activity.");
  expect(requests[0]).toContain("category=comment");
  expect(requests[0]).toContain("unread=true");
});
