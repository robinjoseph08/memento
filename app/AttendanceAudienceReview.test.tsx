import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { AttendanceAudienceReview } from "./AttendanceAudienceReview";
import type { Review } from "./types/generated/audiences";

const momentID = "11111111-1111-4111-8111-111111111111";
const attendeeID = "22222222-2222-4222-8222-222222222222";
const recipientID = "33333333-3333-4333-8333-333333333333";
const csrfToken = "c".repeat(64);

function review(): Review {
  return {
    target_kind: "moment",
    target_id: momentID,
    version: 1,
    attendance_confirmed: false,
    audience_complete: false,
    people: [{ id: attendeeID, display_name: "Alex", sort_name: "Alex" }],
    eligible_recipients: [
      { id: recipientID, display_name: "Bailey", sort_name: "Bailey" },
    ],
    attendance: [],
    face_evidence: [
      {
        media_item_id: "44444444-4444-4444-8444-444444444444",
        evidence_id: "evidence-known",
        image_width: 100,
        image_height: 80,
        x1: 1,
        y1: 2,
        x2: 20,
        y2: 30,
        suggested_person: {
          id: attendeeID,
          display_name: "Alex",
          sort_name: "Alex",
        },
      },
      {
        media_item_id: "44444444-4444-4444-8444-444444444444",
        evidence_id: "evidence-unknown",
        image_width: 100,
        image_height: 80,
        x1: 2,
        y1: 3,
        x2: 21,
        y2: 31,
        suggested_person: null,
      },
    ],
    face_evidence_available: true,
    proposal: [
      {
        recipient: {
          id: recipientID,
          display_name: "Bailey",
          sort_name: "Bailey",
        },
        included: true,
        reasons: [
          {
            kind: "present",
            matching_person: {
              id: recipientID,
              display_name: "Bailey",
              sort_name: "Bailey",
            },
          },
          {
            kind: "interested",
            matching_person: {
              id: attendeeID,
              display_name: "Alex",
              sort_name: "Alex",
            },
          },
        ],
      },
    ],
    approved_audience: null,
  };
}

function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function pathOf(input: RequestInfo | URL) {
  return typeof input === "string"
    ? input
    : input instanceof URL
      ? input.href
      : input.url;
}

function renderReview(onAttendance = vi.fn(), onAudience = vi.fn()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <AttendanceAudienceReview
        csrfToken={csrfToken}
        momentID={momentID}
        onAttendanceConfirmed={onAttendance}
        onAudienceChanged={() => undefined}
        onAudienceApproved={onAudience}
      />
    </QueryClientProvider>,
  );
  return { onAttendance, onAudience };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("requires explicit Attendance confirmation and shows advisory face evidence", async () => {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      requests.push({ path, init });
      if (path.endsWith("/attendance-audience")) return response(review());
      if (path.endsWith("/attendance") && init?.method === "PUT") {
        const result = review();
        result.version = 2;
        result.attendance_confirmed = true;
        result.attendance = result.people;
        return response(result);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const { onAttendance } = renderReview();

  expect(
    await screen.findByText(
      "Alex suggested on Media 44444444. Review only, not access.",
    ),
  ).toBeInTheDocument();
  expect(
    screen.getByText(
      "Unmatched face on Media 44444444. Review only, not access.",
    ),
  ).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Alex" })).not.toBeChecked();
  expect(screen.getByText("Present: Bailey")).toBeInTheDocument();
  expect(screen.getByText("Interested: Alex")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("checkbox", { name: "Alex" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm Attendance" }));
  await waitFor(() => expect(onAttendance).toHaveBeenCalledOnce());
  const request = requests.find(({ path }) => path.endsWith("/attendance"));
  expect(request?.init?.headers).toMatchObject({
    "If-Match": "1",
    "X-Memento-CSRF": csrfToken,
  });
  expect(JSON.parse(request?.init?.body as string)).toEqual({
    person_ids: [attendeeID],
  });
});

test("retains explained manual overrides and explicitly approves a snapshot", async () => {
  let current = review();
  current.attendance_confirmed = true;
  const overrideBodies: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path.endsWith("/attendance-audience")) return response(current);
      if (path.endsWith("/audience/override")) {
        const request = JSON.parse(init?.body as string) as {
          recipient_person_id: string;
          state: string;
        };
        overrideBodies.push(request);
        current = {
          ...current,
          version: current.version + 1,
          proposal:
            request.state === "automatic"
              ? []
              : [
                  {
                    ...current.proposal[0],
                    included: false,
                    reasons: [
                      ...current.proposal[0].reasons,
                      { kind: "manually_excluded", matching_person: null },
                    ],
                  },
                ],
        };
        return response(current);
      }
      if (path.endsWith("/audience/approve"))
        return response({
          version: current.version + 1,
          audience: {
            id: "77777777-7777-4777-8777-777777777777",
            label: "Curator only",
            recipients: [],
            approved_at: "2026-01-01T00:00:00Z",
          },
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const { onAudience } = renderReview();

  const recipient = await screen.findByRole("checkbox", { name: "Bailey" });
  fireEvent.click(recipient);
  expect(await screen.findByText("Manually excluded")).toBeInTheDocument();
  expect(overrideBodies).toEqual([
    { recipient_person_id: recipientID, state: "excluded" },
  ]);
  fireEvent.click(
    screen.getByRole("button", {
      name: "Use automatic proposal for Bailey",
    }),
  );
  expect(
    await screen.findByText("No Recipients proposed."),
  ).toBeInTheDocument();
  expect(overrideBodies).toEqual([
    { recipient_person_id: recipientID, state: "excluded" },
    { recipient_person_id: recipientID, state: "automatic" },
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Approve Curator only" }));
  const approvedLabel = await screen.findByText(/Approved snapshot:/);
  expect(approvedLabel.closest("p")).toHaveTextContent(
    "Curator only (0 Recipients). It will not recalculate later.",
  );
  expect(onAudience).toHaveBeenCalledOnce();
});

test("reloads the latest review after an optimistic conflict", async () => {
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path.endsWith("/attendance-audience")) {
        reads += 1;
        const latest = review();
        latest.version = reads;
        return response(latest);
      }
      if (path.endsWith("/attendance") && init?.method === "PUT") {
        return response(
          { error: { message: "This review changed in another browser." } },
          409,
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  renderReview();
  await screen.findByRole("checkbox", { name: "Alex" });
  fireEvent.click(screen.getByRole("button", { name: "Confirm Attendance" }));
  expect(
    await screen.findByText("This review changed in another browser."),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Load latest review" }));
  await waitFor(() => expect(reads).toBe(2));
  expect(
    screen.queryByText("This review changed in another browser."),
  ).not.toBeInTheDocument();
});
