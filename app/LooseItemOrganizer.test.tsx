import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { LooseItemOrganizer } from "./LooseItemOrganizer";
import { CURRENT_SESSION_QUERY_KEY } from "./hooks/queries/sessions";
import type { LooseItem } from "./types/generated/events";

const csrf = "c".repeat(64);
const looseID = "11111111-1111-4111-8111-111111111111";
const personID = "22222222-2222-4222-8222-222222222222";

function item(overrides: Partial<LooseItem> = {}): LooseItem {
  return {
    id: looseID,
    lifecycle: "draft",
    title: "Garden portrait",
    description: "",
    grouping_timezone: "UTC",
    proposed_day: "2026-08-01",
    place_labels: ["Garden"],
    version: 1,
    audience_complete: false,
    published_editable_version: null,
    has_staged_update: false,
    pending_withdrawal_publication: false,
    withdrawal_targets: [],
    withdrawals: [],
    media_item: {
      id: "media-1",
      media_type: "image",
      width: 1200,
      height: 800,
      local_date_time: null,
    },
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

function json(value: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function ExternalLooseNavigation() {
  const navigate = useNavigate();
  return (
    <>
      <button
        onClick={() => void navigate("/?workspace=drafts&loose=loose-2")}
        type="button"
      >
        Visit second Loose item
      </button>
      <button
        onClick={() => void navigate(`/?workspace=drafts&loose=${looseID}`)}
        type="button"
      >
        Revisit first Loose item
      </button>
    </>
  );
}

function renderOrganizer(externalNavigation = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  client.setQueryData(CURRENT_SESSION_QUERY_KEY, { csrf_token: csrf });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/?workspace=drafts&loose=${looseID}`]}>
        {externalNavigation ? <ExternalLooseNavigation /> : null}
        <LooseItemOrganizer
          session={{
            display_name: "Robin",
            session_type: "trusted",
            csrf_token: csrf,
            curator: true,
            onboarding_required: false,
          }}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("reviews an explicit empty Loose Audience without Attendance and previews Pending Recipient read only", async () => {
  let current = item();
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      requests.push({ path, init });
      if (path === "/api/loose-items")
        return json({
          loose_items: [
            {
              id: looseID,
              lifecycle: current.lifecycle,
              title: current.title,
              version: current.version,
              audience_complete: current.audience_complete,
              has_staged_update: current.has_staged_update,
              updated_at: current.updated_at,
            },
          ],
        });
      if (path === `/api/loose-items/${looseID}`) return json(current);
      if (path === `/api/loose-items/${looseID}/attendance-audience`) {
        return json({
          target_kind: "loose_item",
          target_id: looseID,
          version: current.audience_complete ? 2 : 1,
          attendance_confirmed: true,
          audience_complete: current.audience_complete,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: false,
          proposal: [],
          approved_audience: current.audience_complete
            ? {
                id: "audience-1",
                label: "Curator only",
                recipients: [],
                approved_at: "2026-08-01T00:00:00Z",
              }
            : null,
        });
      }
      if (path === `/api/loose-items/${looseID}/audience/approve`) {
        current = { ...current, version: 2, audience_complete: true };
        return json({
          version: 2,
          audience: {
            id: "audience-1",
            label: "Curator only",
            recipients: [],
            approved_at: "2026-08-01T00:00:00Z",
          },
        });
      }
      if (path === `/api/loose-items/${looseID}/preview-recipients`)
        return json({
          recipients: [
            {
              person_id: personID,
              access_id: "access-1",
              name: "Alex",
              access_state: "onboarding",
            },
          ],
        });
      if (path.startsWith(`/api/loose-items/${looseID}/preview?`))
        return json({
          authorized: true,
          loose_item_id: looseID,
          publication_id: "publication-1",
          title: current.title,
          description: "",
          place_labels: ["Garden"],
          proposed_day: "2026-08-01",
          media: { ...current.media_item, available: true },
          preview: true,
          capabilities: {
            comments: false,
            favorites: false,
            settings: false,
            downloads: false,
            record_engagement: false,
          },
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  renderOrganizer();

  expect(
    await screen.findByText(/Loose items have no Attendance/),
  ).toBeVisible();
  expect(screen.queryByText("Confirmed Attendance")).not.toBeInTheDocument();
  expect(
    screen.getByText(
      /explicit empty Audience to keep this Loose item Curator only/,
    ),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Approve Curator only" }));
  await waitFor(() =>
    expect(screen.getByText(/Approved snapshot:/)).toBeVisible(),
  );
  const approval = requests.find(({ path }) =>
    path.endsWith("/audience/approve"),
  );
  expect(approval?.init?.method).toBe("POST");
  expect(new Headers(approval?.init?.headers).get("X-Memento-CSRF")).toBe(csrf);
  expect(new Headers(approval?.init?.headers).get("If-Match")).toBe("1");

  const recipient = await screen.findByLabelText("Preview Recipient");
  fireEvent.change(recipient, { target: { value: personID } });
  expect(
    screen.getByText(/Pending Recipient: cannot access yet/),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Preview as Recipient" }));
  const preview = await screen.findByRole("region", {
    name: "Read-only Recipient preview",
  });
  await within(preview).findByText("1 authorized Media item");
  expect(preview).toHaveTextContent(
    "Preview activity is not recorded as Recipient engagement.",
  );
  for (const action of ["Comment", "Favorite", "Settings", "Download"])
    expect(
      within(preview).getByRole("button", { name: action }),
    ).toBeDisabled();
});

test("blocks URL navigation until authoritative Audience recovery finishes", async () => {
  let firstDetailLoads = 0;
  let resolveStale!: (looseItem: LooseItem) => void;
  const staleAuthority = new Promise<LooseItem>((resolve) => {
    resolveStale = resolve;
  });
  const publishedItem = (id: string, title: string) =>
    item({
      id,
      title,
      lifecycle: "published",
      published_editable_version: 1,
      withdrawal_targets: [
        { target_kind: "loose_item", target_id: id, label: title },
      ],
    });
  const second = publishedItem("loose-2", "Second Loose item");
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      if (path === "/api/loose-items")
        return json({
          loose_items: [
            {
              id: looseID,
              lifecycle: "published",
              title: "First visit",
              version: 1,
              audience_complete: false,
              has_staged_update: false,
              updated_at: "2026-08-01T00:00:00Z",
            },
            {
              id: second.id,
              lifecycle: "published",
              title: second.title,
              version: 1,
              audience_complete: false,
              has_staged_update: false,
              updated_at: "2026-08-01T00:00:00Z",
            },
          ],
        });
      if (path === `/api/loose-items/${looseID}`) {
        firstDetailLoads += 1;
        if (firstDetailLoads === 1)
          return json(publishedItem(looseID, "First visit"));
        return staleAuthority.then((looseItem) => json(looseItem));
      }
      if (path === `/api/loose-items/${second.id}`) return json(second);
      if (path.endsWith("/attendance-audience"))
        return json({
          target_kind: "loose_item",
          target_id: path.includes(second.id) ? second.id : looseID,
          version: 1,
          attendance_confirmed: true,
          audience_complete: false,
          people: [],
          eligible_recipients: [],
          attendance: [],
          face_evidence: [],
          face_evidence_available: false,
          proposal: [],
          approved_audience: null,
        });
      if (path.endsWith("/audience/approve") && init?.method === "POST")
        return json({
          version: 2,
          audience: {
            id: "audience-1",
            label: "Curator only",
            recipients: [],
            approved_at: "2026-08-01T00:00:00Z",
          },
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  renderOrganizer(true);
  await screen.findByRole("heading", { name: "First visit" });
  fireEvent.change(screen.getByLabelText("Attributable reason"), {
    target: { value: "Reason for the first item" },
  });

  fireEvent.click(
    await screen.findByRole("button", { name: "Approve Curator only" }),
  );
  await waitFor(() => expect(firstDetailLoads).toBeGreaterThanOrEqual(2));
  await screen.findByText(
    "Reload the authoritative Loose item before Preview, Withdrawal, or Publication can continue.",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Visit second Loose item" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("heading", { name: "First visit" })).toBeVisible(),
  );
  expect(
    screen.queryByRole("heading", { name: "Second Loose item" }),
  ).not.toBeInTheDocument();

  resolveStale(publishedItem(looseID, "Stale first visit"));
  await waitFor(() =>
    expect(
      screen.queryByText(
        "Reload the authoritative Loose item before Preview, Withdrawal, or Publication can continue.",
      ),
    ).not.toBeInTheDocument(),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Visit second Loose item" }),
  );
  await screen.findByRole("heading", { name: "Second Loose item" });
  expect(screen.getByLabelText("Attributable reason")).toHaveValue("");
});
