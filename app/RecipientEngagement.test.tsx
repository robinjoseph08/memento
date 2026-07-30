import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { RecipientEngagement } from "./RecipientEngagement";

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
  cleanup();
  vi.restoreAllMocks();
});

test("identifies explicitly opened Media and exposes its Recipient openers", async () => {
  const mediaID = "11111111-1111-1111-1111-111111111111";
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = requestPath(input);
      if (path.startsWith("/api/engagement/recipients/recipient-id?")) {
        return json({
          recipient_person_id: "recipient-id",
          latest_meaningful_activity_at: "2026-07-30T17:00:00Z",
          active_days: { days_7: 1, days_30: 1, days_90: 1 },
          visit_frequency: {
            visits_30_days: 1,
            active_visit_days_30: 1,
            visits_per_active_day_30: 1,
          },
          counts_90_days: {
            event_opens: 0,
            media_opens: 1,
            video_starts: 0,
            downloads: 0,
            comments: 0,
            favorite_changes: 0,
            invitation_suggestions: 0,
          },
          timeline: [
            {
              id: "1",
              kind: "media_opened",
              target_kind: "media",
              target_id: mediaID,
              target_label: "Media item",
              occurred_at: "2026-07-30T17:00:00Z",
            },
          ],
          next_cursor: null,
        });
      }
      if (path === `/api/engagement/media/${mediaID}/openers`) {
        return json({
          media_item_id: mediaID,
          openers: [
            {
              recipient_person_id: "recipient-id",
              recipient_name: "Alex Recipient",
              open_count: 2,
              first_opened_at: "2026-07-29T17:00:00Z",
              latest_opened_at: "2026-07-30T17:00:00Z",
            },
          ],
        });
      }
      return Promise.reject(new Error(`Unexpected request: ${path}`));
    }),
  );

  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RecipientEngagement personID="recipient-id" />
    </QueryClientProvider>,
  );

  await screen.findByText("Latest activity");
  fireEvent.click(screen.getByText("Recent engagement timeline"));
  expect(screen.getByText(`Media ${mediaID}`, { exact: false })).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Inspect Media openers" }),
  );
  expect(
    await screen.findByText(/Alex Recipient: 2 explicit opens/),
  ).toBeVisible();
});
