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
import { CURRENT_SESSION_QUERY_KEY } from "./hooks/queries/sessions";
import type { LooseItem } from "./types/generated/events";

const csrf = "c".repeat(64);
const looseID = "11111111-1111-4111-8111-111111111111";
const looseItem: LooseItem = {
  id: looseID,
  lifecycle: "published",
  title: "Composed Loose item",
  description: "",
  grouping_timezone: "UTC",
  proposed_day: "2026-08-01",
  place_labels: ["Garden"],
  version: 2,
  audience_complete: true,
  published_editable_version: 2,
  has_staged_update: false,
  pending_withdrawal_publication: false,
  withdrawal_targets: [
    { target_kind: "loose_item", target_id: looseID, label: "Composed" },
  ],
  withdrawals: [],
  media_item: {
    id: "22222222-2222-4222-8222-222222222222",
    media_type: "image",
    width: 1,
    height: 1,
    local_date_time: null,
  },
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

function bodyText(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected a JSON request body");
  return body;
}

function json(value: unknown) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function CrossKindNavigation() {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <>
      <button onClick={() => void navigate("/?workspace=drafts&event=event-1")}>
        Navigate to Events externally
      </button>
      <output aria-label="Composed search">{location.search}</output>
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

test("the composed workspace confirms cross-kind dirty navigation exactly once", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.pathname
            : new URL(input.url).pathname;
      if (path === "/api/loose-items")
        return json({
          loose_items: [
            {
              id: looseID,
              lifecycle: "published",
              title: looseItem.title,
              version: looseItem.version,
              audience_complete: true,
              has_staged_update: false,
              updated_at: looseItem.updated_at,
            },
          ],
        });
      if (path === `/api/loose-items/${looseID}` && init?.method === "PUT") {
        const request = JSON.parse(bodyText(init.body)) as { title: string };
        return json({ ...looseItem, title: request.title, version: 3 });
      }
      if (path === `/api/loose-items/${looseID}`) return json(looseItem);
      if (path.endsWith("/attendance-audience"))
        return json({
          target_kind: "loose_item",
          target_id: looseID,
          version: 2,
          attendance_confirmed: true,
          audience_complete: true,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: false,
          proposal: [],
          approved_audience: {
            id: "audience-1",
            label: "Curator only",
            recipients: [],
            approved_at: "2026-08-01T00:00:00Z",
          },
        });
      if (path.endsWith("/preview-recipients")) return json({ recipients: [] });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, { csrf_token: csrf });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/?workspace=drafts&loose=${looseID}`]}>
        <CrossKindNavigation />
        <CurationWorkspace
          session={{
            display_name: "Curator",
            session_type: "trusted",
            csrf_token: csrf,
            curator: true,
            onboarding_required: false,
          }}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );

  await screen.findByRole("heading", { name: "Composed Loose item" });
  fireEvent.change(screen.getByLabelText("Loose item title"), {
    target: { value: "Unsaved composed edit" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Navigate to Events externally" }),
  );

  await waitFor(() => expect(confirm).toHaveBeenCalledOnce());
  expect(
    screen.getByRole("heading", { name: "Unsaved composed edit" }),
  ).toBeVisible();
  expect(screen.queryByText("Organize drafts")).not.toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByLabelText("Composed search")).toHaveTextContent(
      `?workspace=drafts&loose=${looseID}`,
    ),
  );
});
