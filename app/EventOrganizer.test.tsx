import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { afterEach, expect, test, vi } from "vitest";

import { EventOrganizer } from "./EventOrganizer";
import { eventKeys } from "./hooks/queries/events";
import { problemResponse } from "./test/problem";
import type {
  Event as DraftEvent,
  MediaItem,
  OrganizeEventRequest,
  RestorePublishedMediaRequest,
  WithdrawRequest,
} from "./types/generated/events";

const csrfToken = "c".repeat(64);
const eventID = "11111111-1111-4111-8111-111111111111";
const momentOneID = "22222222-2222-4222-8222-222222222222";
const momentTwoID = "33333333-3333-4333-8333-333333333333";
const deletedMomentID = "44444444-4444-4444-8444-444444444444";
const contentionWait = { timeout: 5_000 };

type OrganizerAPIOptions = {
  attendanceGate?: Promise<void>;
  organizationGate?: Promise<void>;
  restoreGate?: Promise<void>;
  restoreMomentID?: string;
  restoreAsCover?: boolean;
  restoreConflictOnce?: boolean;
  restoreConflictGate?: Promise<void>;
  restoreVersions?: number[];
  publicationRefetchGate?: Promise<void>;
  withdrawalGate?: Promise<void>;
  withdrawalRefetchGate?: Promise<void>;
  withdrawalRefetchFailures?: number;
};

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
}

function media(id: string, mediaType: string): MediaItem {
  return {
    id,
    media_type: mediaType,
    width: 1200,
    height: 800,
    local_date_time: null,
  };
}

const items = {
  a: media("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "first photo"),
  b: media("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "second photo"),
  c: media("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "third photo"),
  loose: media("dddddddd-dddd-4ddd-8ddd-dddddddddddd", "loose photo"),
};

function draft(version = 1): DraftEvent {
  return {
    id: eventID,
    lifecycle: "draft",
    title: "Family weekend",
    description: "",
    place_labels: [],
    grouping_timezone: "UTC",
    version,
    final_review_complete: false,
    published_editable_version: null,
    published_attendance_recovery_required: false,
    pending_withdrawal_publication: false,
    staged_update: null,
    sources: [],
    moments: [
      {
        id: momentOneID,
        title: "Friday",
        place_labels: [],
        proposed_day: "2026-05-01",
        grouping_timezone: "UTC",
        source_days: ["2026-05-01"],
        proposal_kind: "local_day",
        cover_media_item_id: items.a.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.a],
      },
      {
        id: momentTwoID,
        title: "Saturday",
        place_labels: [],
        proposed_day: "2026-05-02",
        grouping_timezone: "UTC",
        source_days: ["2026-05-02"],
        proposal_kind: "local_day",
        cover_media_item_id: items.b.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.b, items.c],
      },
    ],
    unassigned_media: [items.loose],
    withdrawal_targets: [
      {
        target_kind: "event",
        target_id: eventID,
        label: "Event: Family weekend",
      },
      {
        target_kind: "moment",
        target_id: momentOneID,
        label: "Moment: Friday",
      },
      {
        target_kind: "moment",
        target_id: momentTwoID,
        label: "Moment: Saturday",
      },
      {
        target_kind: "media",
        target_id: items.a.id,
        label: "Media: first photo",
      },
      {
        target_kind: "media",
        target_id: items.b.id,
        label: "Media: second photo",
      },
      {
        target_kind: "media",
        target_id: items.c.id,
        label: "Media: third photo",
      },
    ],
    withdrawals: [],
    created_at: "2026-05-03T00:00:00Z",
    updated_at: "2026-05-03T00:00:00Z",
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

function stringBody(body: BodyInit | null | undefined) {
  if (typeof body !== "string") throw new Error("Expected a JSON request body");
  return body;
}

function eventFromRequest(
  request: OrganizeEventRequest,
  baseline = draft(request.version),
): DraftEvent {
  const byID = new Map(Object.values(items).map((item) => [item.id, item]));
  const priorMoments = new Map(
    baseline.moments.map((moment) => [moment.id, moment]),
  );
  const existing = structuredClone(baseline);
  existing.version = request.version + 1;
  existing.title = request.title ?? baseline.title;
  existing.description = request.description ?? baseline.description;
  existing.grouping_timezone =
    request.grouping_timezone ?? baseline.grouping_timezone;
  existing.final_review_complete = request.final_review_complete;
  existing.place_labels = request.place_labels;
  existing.moments = request.moments.map((moment) => ({
    id: moment.id,
    title: moment.title ?? "",
    place_labels: moment.place_labels,
    proposed_day: moment.proposed_day,
    grouping_timezone: "UTC",
    source_days: [moment.proposed_day],
    proposal_kind: "local_day",
    cover_media_item_id: moment.cover_media_item_id,
    attendance_complete:
      priorMoments.get(moment.id)?.attendance_complete ?? false,
    audience_complete: priorMoments.get(moment.id)?.audience_complete ?? false,
    media_items: moment.media_item_ids.map((id) => byID.get(id)!),
  }));
  existing.unassigned_media = request.unassigned_media_ids.map((id) =>
    byID.get(id)!,
  );
  return existing;
}

function organizedDraft(version = 1): DraftEvent {
  const event = draft(version);
  event.moments = [
    {
      ...event.moments[1],
      proposed_day: "2026-05-01",
      source_days: ["2026-05-01"],
      cover_media_item_id: items.b.id,
      media_items: [items.b],
    },
    {
      ...event.moments[0],
      cover_media_item_id: items.loose.id,
      media_items: [items.c, items.a, items.loose],
    },
  ];
  event.unassigned_media = [];
  return event;
}

function emptyReview(momentID: string) {
  return {
    target_kind: "moment",
    target_id: momentID,
    version: 1,
    attendance_confirmed: false,
    audience_complete: false,
    people: [],
    eligible_recipients: [],
    attendance: [],
    face_evidence: [],
    face_evidence_available: true,
    proposal: [],
    approved_audience: null,
  };
}

function stubOrganizerAPI(
  initial: DraftEvent,
  options: OrganizerAPIOptions = {},
) {
  let persisted = initial;
  let publicationCommitted = false;
  let restorationConflictReturned = false;
  let restorationRefetched = false;
  let withdrawalCommitted = false;
  let withdrawalRefetchFailures = options.withdrawalRefetchFailures ?? 0;
  const saves: OrganizeEventRequest[] = [];
  const reviews = new Map<string, ReturnType<typeof emptyReview>>();
  const reviewFor = (momentID: string) => {
    let current = reviews.get(momentID);
    if (!current) {
      current = emptyReview(momentID);
      reviews.set(momentID, current);
    }
    return current;
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method) {
        return response({
          events: [
            {
              id: eventID,
              title: persisted.title,
              version: persisted.version,
              moment_count: persisted.moments.length,
              unassigned_count: persisted.unassigned_media.length,
              has_staged_update: persisted.staged_update !== null,
              lifecycle: persisted.lifecycle,
              updated_at: persisted.updated_at,
            },
          ],
        });
      }
      if (
        path === `/api/events/${eventID}/publications` &&
        init?.method === "POST"
      ) {
        expect(init.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        expect(JSON.parse(stringBody(init.body))).toEqual({
          version: persisted.version,
          notify_recipients: true,
        });
        const publicationID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee";
        persisted = {
          ...persisted,
          lifecycle: "published",
          published_editable_version: persisted.version,
          published_attendance_recovery_required: false,
          pending_withdrawal_publication: false,
          staged_update: null,
          withdrawals: persisted.withdrawals.map((withdrawal) =>
            withdrawal.restored_at
              ? withdrawal
              : {
                  ...withdrawal,
                  restored_by_publication_id: publicationID,
                  restored_at: "2026-05-03T02:00:00Z",
                },
          ),
        };
        publicationCommitted = true;
        return response(
          {
            id: publicationID,
            event_id: eventID,
            revision: 1,
            editable_version: persisted.version,
            notify_recipients: true,
            committed_at: "2026-05-03T00:00:00Z",
          },
          201,
        );
      }
      if (path === `/api/events/${eventID}/preview-recipients`) {
        return response({
          recipients: [
            {
              person_id: "ffffffff-ffff-4fff-8fff-ffffffffffff",
              access_id: "99999999-9999-4999-8999-999999999999",
              name: "Alex",
              access_state: "onboarding",
            },
            {
              person_id: "abababab-abab-4bab-8bab-abababababab",
              access_id: "89898989-8989-4989-8989-898989898989",
              name: "Bailey",
              access_state: "completed",
            },
          ],
        });
      }
      if (path.startsWith(`/api/events/${eventID}/preview?`)) {
        expect(init?.method).toBe("POST");
        expect(init?.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        return response({
          authorized: true,
          event_id: eventID,
          publication_id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
          title: "Family weekend",
          description: "",
          cover_media_id: items.a.id,
          media_count: 1,
          media: [{ ...items.a, available: true }],
          preview: true,
          capabilities: {
            comments: false,
            favorites: false,
            settings: false,
            downloads: false,
            record_engagement: false,
          },
        });
      }
      if (path === "/api/withdrawals" && init?.method === "POST") {
        expect(init.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        const request = JSON.parse(stringBody(init.body)) as WithdrawRequest;
        expect(request.reason).toBe("Privacy request");
        persisted = {
          ...persisted,
          version: persisted.version + 1,
          final_review_complete: false,
          moments: persisted.moments.map((moment) => ({
            ...moment,
            audience_complete: false,
          })),
          withdrawal_targets: persisted.withdrawal_targets.filter(
            (target) =>
              target.target_kind !== request.target_kind ||
              target.target_id !== request.target_id,
          ),
          withdrawals: [
            {
              id: "77777777-7777-4777-8777-777777777777",
              target_kind: request.target_kind,
              target_id: request.target_id,
              reason: request.reason,
              withdrawn_by_name: "Robin",
              withdrawn_at: "2026-05-03T00:00:00Z",
              restored_by_publication_id: null,
              restored_at: null,
              affected_recipient_count: 2,
              affected_media_count: 3,
              affected_event_count: 1,
            },
          ],
        };
        withdrawalCommitted = true;
        const withdrawal = structuredClone(persisted.withdrawals[0]);
        return options.withdrawalGate
          ? options.withdrawalGate.then(() => response(withdrawal, 201))
          : response(withdrawal, 201);
      }
      if (
        path === `/api/events/${eventID}/published-media-restorations` &&
        init?.method === "POST"
      ) {
        expect(init.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        const request = JSON.parse(
          stringBody(init.body),
        ) as RestorePublishedMediaRequest;
        options.restoreVersions?.push(request.version);
        expect(request.version).toBe(persisted.version);
        if (options.restoreConflictOnce && !restorationConflictReturned) {
          persisted = {
            ...persisted,
            version: persisted.version + 1,
            title: "Newer server Event",
            description: "Newer server description",
          };
          restorationConflictReturned = true;
          const conflict = response(
            problemResponse(
              "This Event changed in another browser. Review the newer version before saving again.",
              409,
            ),
            409,
          );
          return options.restoreConflictGate
            ? options.restoreConflictGate.then(() => conflict)
            : conflict;
        }
        if (restorationConflictReturned && !restorationRefetched) {
          throw new Error("Restoration retried before loading the newer Event");
        }
        const restored = Object.values(items).find(
          (item) => item.id === request.media_item_id,
        );
        if (!restored) throw new Error("Unknown restored Media fixture");
        persisted = {
          ...persisted,
          version: persisted.version + 1,
          staged_update: null,
          moments: persisted.moments.map((moment) =>
            moment.id === options.restoreMomentID
              ? {
                  ...moment,
                  cover_media_item_id: options.restoreAsCover
                    ? restored.id
                    : moment.cover_media_item_id,
                  media_items: [...moment.media_items, restored],
                }
              : moment,
          ),
          unassigned_media: options.restoreMomentID
            ? persisted.unassigned_media
            : [...persisted.unassigned_media, restored],
        };
        const restoration = structuredClone(persisted);
        return options.restoreGate
          ? options.restoreGate.then(() => response(restoration))
          : response(restoration);
      }
      if (path === `/api/events/${eventID}`) {
        if (withdrawalCommitted && options.withdrawalRefetchGate)
          return options.withdrawalRefetchGate.then(() => response(persisted));
        if (withdrawalCommitted && withdrawalRefetchFailures > 0) {
          withdrawalRefetchFailures -= 1;
          return response(
            problemResponse(
              "The Event reload is temporarily unavailable.",
              503,
            ),
            503,
          );
        }
        if (restorationConflictReturned) restorationRefetched = true;
        if (publicationCommitted && options.publicationRefetchGate) {
          return options.publicationRefetchGate.then(() => response(persisted));
        }
        return response(persisted);
      }
      const reviewMatch = path.match(
        /^\/api\/moments\/([^/]+)\/attendance-audience$/,
      );
      if (reviewMatch) return response(reviewFor(reviewMatch[1]));
      const attendanceMatch = path.match(
        /^\/api\/moments\/([^/]+)\/attendance$/,
      );
      if (attendanceMatch && init?.method === "PUT") {
        const review = reviewFor(attendanceMatch[1]);
        expect(init.headers).toMatchObject({
          "If-Match": String(review.version),
          "X-Memento-CSRF": csrfToken,
        });
        const confirm = () => {
          const moment = persisted.moments.find(
            (candidate) => candidate.id === attendanceMatch[1],
          );
          if (moment) {
            moment.attendance_complete = true;
            moment.audience_complete = false;
          }
          persisted.version += 1;
          persisted.final_review_complete = false;
          review.version += 1;
          review.attendance_confirmed = true;
          review.audience_complete = false;
          return response(review);
        };
        return options.attendanceGate
          ? options.attendanceGate.then(confirm)
          : confirm();
      }
      const approvalMatch = path.match(
        /^\/api\/moments\/([^/]+)\/audience\/approve$/,
      );
      if (approvalMatch && init?.method === "POST") {
        const review = reviewFor(approvalMatch[1]);
        expect(init.headers).toMatchObject({
          "If-Match": String(review.version),
          "X-Memento-CSRF": csrfToken,
        });
        const moment = persisted.moments.find(
          (candidate) => candidate.id === approvalMatch[1],
        );
        if (moment) moment.audience_complete = true;
        persisted.version += 1;
        persisted.final_review_complete = false;
        review.version += 1;
        review.audience_complete = true;
        return response({
          version: review.version,
          audience: {
            id: crypto.randomUUID(),
            label: "Curator only",
            recipients: [],
            approved_at: "2026-05-03T00:00:00Z",
          },
        });
      }
      if (
        path === `/api/events/${eventID}/organization` &&
        init?.method === "PUT"
      ) {
        expect(init.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        const request = JSON.parse(
          stringBody(init.body),
        ) as OrganizeEventRequest;
        saves.push(request);
        persisted = eventFromRequest(request, persisted);
        const saved = structuredClone(persisted);
        return options.organizationGate
          ? options.organizationGate.then(() => response(saved))
          : response(saved);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  return saves;
}

function ExternalNavigation() {
  const navigate = useNavigate();
  return (
    <button
      onClick={() => void navigate("/?event=external-event")}
      type="button"
    >
      Navigate externally
    </button>
  );
}

function renderOrganizer(initialEntry = "/") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const rendered = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <ExternalNavigation />
        <EventOrganizer
          session={{
            display_name: "Robin",
            session_type: "public",
            csrf_token: csrfToken,
            curator: true,
            onboarding_required: false,
          }}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...rendered, client };
}

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", window.location.pathname);
  vi.useRealTimers();
  vi.restoreAllMocks();
});

test("shows the resulting Event with highlighted Staged net changes", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.title = "Corrected family weekend";
  staged.description = "The complete corrected description";
  staged.place_labels = ["Coastal overlook", "Garden terrace"];
  staged.grouping_timezone = "America/New_York";
  staged.moments.find((moment) => moment.id === momentOneID)!.place_labels = [
    "Breakfast room",
  ];
  staged.published_editable_version = 7;
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "addition",
        count: 1,
        media_item_ids: [items.b.id],
        moment_ids: [],
        detail: "Media added",
      },
      {
        kind: "removal",
        count: 2,
        media_item_ids: [items.c.id, items.loose.id],
        moment_ids: [],
        removed_media: [
          {
            id: items.c.id,
            media_type: items.c.media_type,
            local_date_time: items.c.local_date_time,
            restorable: true,
          },
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: false,
          },
        ],
        detail: "Media removed",
      },
      {
        kind: "move",
        count: 1,
        media_item_ids: [items.a.id],
        moment_ids: [],
        detail: "Media moved or reordered",
      },
      {
        kind: "moment_structure",
        count: 1,
        media_item_ids: [],
        moment_ids: [deletedMomentID],
        deleted_moments: [
          {
            id: deletedMomentID,
            title: "Sunday breakfast",
            proposed_day: "2026-05-03",
          },
        ],
        detail: "Moment structure or ordering changed",
      },
      {
        kind: "metadata",
        count: 3,
        media_item_ids: [items.a.id],
        moment_ids: [momentOneID],
        event_metadata_fields: [
          "title",
          "description",
          "place_labels",
          "grouping_timezone",
        ],
        detail: "Event, Moment, or Media metadata edited",
      },
      {
        kind: "access",
        count: 2,
        media_item_ids: [items.a.id],
        moment_ids: [momentOneID, momentTwoID],
        recipient_access: [
          {
            recipient_person_id: "55555555-5555-4555-8555-555555555555",
            recipient_name: "Alex",
            granted_media_count: 2,
            revoked_media_count: 0,
          },
          {
            recipient_person_id: "66666666-6666-4666-8666-666666666666",
            recipient_name: "Jamie",
            granted_media_count: 0,
            revoked_media_count: 1,
          },
        ],
        detail: "Recipient Media access granted or revoked",
      },
    ],
  };
  stubOrganizerAPI(staged);
  renderOrganizer();

  const workItem = await screen.findByRole(
    "button",
    { name: /Corrected family weekend/ },
    contentionWait,
  );
  expect(workItem).toHaveTextContent("Staged update");
  fireEvent.click(workItem);

  const review = await screen.findByRole("region", {
    name: "Staged update review",
  });
  expect(review).toHaveTextContent(
    "Event details and organization that will replace the current Publication",
  );
  const eventMetadata = within(review).getByRole("region", {
    name: "Event details that will publish",
  });
  expect(eventMetadata).toHaveTextContent("Corrected family weekend");
  expect(eventMetadata).toHaveTextContent("The complete corrected description");
  expect(eventMetadata).toHaveTextContent("Coastal overlook, Garden terrace");
  expect(eventMetadata).toHaveTextContent("America/New_York");
  expect(eventMetadata.querySelectorAll(".staged-metadata")).toHaveLength(4);
  expect(
    within(eventMetadata).getAllByText("Staged: Metadata edits"),
  ).toHaveLength(4);
  expect(screen.getByLabelText("Place labels for Moment 2")).toHaveValue(
    "Breakfast room",
  );
  const netChangeSummary = within(review).getByRole("list", {
    name: "Net change summary",
  });
  expect(
    within(netChangeSummary).getByText("Additions").closest("li"),
  ).toHaveTextContent("1 Media item");
  expect(
    within(netChangeSummary).getByText("Removals").closest("li"),
  ).toHaveTextContent("2 Media items");
  expect(
    within(netChangeSummary).getByText("Moves and ordering").closest("li"),
  ).toHaveTextContent("1 Media item");
  expect(
    within(netChangeSummary).getByText("Moment structure").closest("li"),
  ).toHaveTextContent("1 Moment");
  expect(
    within(netChangeSummary).getByText("Metadata edits").closest("li"),
  ).toHaveTextContent("1 Event, 1 Moment, and 1 Media item");
  expect(
    within(netChangeSummary).getByText("Access changes").closest("li"),
  ).toHaveTextContent("2 Recipients");
  const recipientAccess = within(review).getByRole("list", {
    name: "Recipient access changes",
  });
  expect(recipientAccess).toHaveTextContent("Alex2 Media granted");
  expect(recipientAccess).toHaveTextContent("Jamie1 Media revoked");
  expect(review).toHaveTextContent("Removed from the resulting Event");
  expect(review).toHaveTextContent("Undated third photo");
  expect(review).toHaveTextContent(items.c.id);
  expect(review).toHaveTextContent("Deleted Moment");
  expect(review).toHaveTextContent("Sunday breakfast");
  expect(review).toHaveTextContent(deletedMomentID);
  expect(review.querySelectorAll(".staged-removal")).toHaveLength(3);
  expect(document.querySelectorAll(".media-row.staged-addition")).toHaveLength(
    1,
  );
  expect(
    document.querySelectorAll(".moment-card.staged-metadata"),
  ).toHaveLength(1);
  expect(document.querySelectorAll(".moment-card.staged-access")).toHaveLength(
    2,
  );
  expect(
    document.querySelectorAll(
      ".media-row.staged-move.staged-metadata.staged-access",
    ),
  ).toHaveLength(1);
  expect(
    screen.getByLabelText(
      "Staged changes: Moves and ordering, Metadata edits, Access changes",
    ),
  ).toHaveTextContent(
    "Staged: Moves and ordering, Metadata edits, Access changes",
  );
  expect(
    screen.getByLabelText("Staged changes: Metadata edits, Access changes"),
  ).toBeVisible();
});

test("summarizes a masked Audience change by its affected Moments", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "access",
        count: 0,
        media_item_ids: [],
        moment_ids: [momentOneID, momentTwoID],
        detail:
          "Moment Audience changed without changing global Recipient Media access",
      },
    ],
  };
  stubOrganizerAPI(staged);
  renderOrganizer();

  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const review = await screen.findByRole("region", {
    name: "Staged update review",
  });
  const accessSummary = within(review)
    .getByText("Access changes")
    .closest("li");
  expect(accessSummary).toHaveTextContent("2 Moments");
  expect(accessSummary).not.toHaveTextContent("0 Recipients");
});

test("restores an autosaved published Media removal from Staged review", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.moments[1].media_items = staged.moments[1].media_items.filter(
    (item) => item.id !== items.loose.id,
  );
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.loose.id],
        moment_ids: [],
        removed_media: [
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: true,
          },
        ],
        detail: "Media removed",
      },
    ],
  };
  stubOrganizerAPI(staged);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Restore removed Media" }),
  );

  await waitFor(() =>
    expect(
      screen.queryByRole("region", { name: "Staged update review" }),
    ).not.toBeInTheDocument(),
  );
  expect(
    screen.getByRole("checkbox", { name: /loose photo/ }),
  ).toBeInTheDocument();
  expect(screen.getByText("All changes saved")).toBeInTheDocument();
});

test("rebases pending local edits before retrying a restoration conflict", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.moments[1].cover_media_item_id = items.a.id;
  staged.moments[1].media_items = staged.moments[1].media_items.filter(
    (item) => item.id !== items.loose.id,
  );
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.loose.id],
        moment_ids: [],
        removed_media: [
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: true,
          },
        ],
        detail: "Media removed",
      },
    ],
  };
  const conflictGate = deferred();
  const restoreVersions: number[] = [];
  const saves = stubOrganizerAPI(staged, {
    restoreConflictOnce: true,
    restoreConflictGate: conflictGate.promise,
    restoreVersions,
  });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Restore removed Media" }),
  );
  expect(
    await screen.findByRole("button", { name: "Restoring…" }),
  ).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: "Edited during conflict recovery" },
  });
  fireEvent.change(screen.getAllByLabelText("Cover")[1], {
    target: { value: items.c.id },
  });
  const eventLabels = screen.getByLabelText("Event Place labels");
  fireEvent.change(eventLabels, {
    target: { value: "Conflict Event Place" },
  });
  fireEvent.blur(eventLabels);
  const momentLabels = screen.getByLabelText("Place labels for Moment 2");
  fireEvent.change(momentLabels, {
    target: { value: "Conflict Moment Place" },
  });
  fireEvent.blur(momentLabels);
  fireEvent.click(
    screen.getByRole("button", { name: "Move Undated first photo earlier" }),
  );

  act(() => conflictGate.resolve());
  expect(
    await screen.findByText(
      "This Event changed in another browser. Load the newer Event before retrying this restoration.",
    ),
  ).toBeInTheDocument();
  expect(saves).toHaveLength(0);

  fireEvent.click(
    screen.getByRole("button", {
      name: "Load newer Event and retry restoration",
    }),
  );

  await waitFor(() =>
    expect(
      screen.queryByRole("region", { name: "Staged update review" }),
    ).not.toBeInTheDocument(),
  );
  expect(restoreVersions).toEqual([8, 9]);
  expect(screen.getByLabelText("Event title")).toHaveValue(
    "Edited during conflict recovery",
  );
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    version: 10,
    title: "Edited during conflict recovery",
    description: "Newer server description",
    place_labels: ["Conflict Event Place"],
    unassigned_media_ids: [items.loose.id],
  });
  expect(saves[0].moments.find((moment) => moment.id === momentOneID)).toEqual(
    expect.objectContaining({
      cover_media_item_id: items.c.id,
      media_item_ids: [items.a.id, items.c.id],
      place_labels: ["Conflict Moment Place"],
    }),
  );
  expect(screen.getByText("All changes saved")).toBeInTheDocument();
});

test("rebases edits made while published Media restoration is pending", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.moments[1].media_items = staged.moments[1].media_items.filter(
    (item) => item.id !== items.loose.id,
  );
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.loose.id],
        moment_ids: [],
        removed_media: [
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: true,
          },
        ],
        detail: "Media removed",
      },
    ],
  };
  staged.moments[1].cover_media_item_id = items.a.id;
  const gate = deferred();
  const saves = stubOrganizerAPI(staged, {
    restoreGate: gate.promise,
    restoreMomentID: momentOneID,
    restoreAsCover: true,
  });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Restore removed Media" }),
  );
  expect(
    await screen.findByRole("button", { name: "Restoring…" }),
  ).toBeDisabled();
  const title = screen.getByLabelText("Event title");
  fireEvent.change(title, { target: { value: "Edited during restoration" } });
  fireEvent.change(screen.getAllByLabelText("Cover")[1], {
    target: { value: items.c.id },
  });
  const eventLabels = screen.getByLabelText("Event Place labels");
  fireEvent.change(eventLabels, {
    target: { value: "Restoration Event Place" },
  });
  fireEvent.blur(eventLabels);
  const momentLabels = screen.getByLabelText("Place labels for Moment 2");
  fireEvent.change(momentLabels, {
    target: { value: "Restoration Moment Place" },
  });
  fireEvent.blur(momentLabels);
  expect(title).toHaveValue("Edited during restoration");

  act(() => gate.resolve());
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: /loose photo/ }),
    ).toBeInTheDocument(),
  );
  expect(title).toHaveValue("Edited during restoration");
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    version: 9,
    title: "Edited during restoration",
    place_labels: ["Restoration Event Place"],
  });
  const restoredMoment = saves[0].moments.find(
    (moment) => moment.id === momentOneID,
  );
  expect(restoredMoment?.cover_media_item_id).toBe(items.c.id);
  expect(restoredMoment?.media_item_ids).toContain(items.loose.id);
  expect(restoredMoment?.place_labels).toEqual(["Restoration Moment Place"]);
});

test("keeps restored Media when its Moment is merged during a delayed restoration", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.moments[0].place_labels = ["Harbor overlook", "Shared table"];
  staged.moments[1].place_labels = ["shared table", "Garden terrace"];
  staged.moments[1].media_items = staged.moments[1].media_items.filter(
    (item) => item.id !== items.loose.id,
  );
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.loose.id],
        moment_ids: [momentOneID],
        removed_media: [
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: true,
          },
        ],
        detail: "Media removed",
      },
    ],
  };
  const gate = deferred();
  const saves = stubOrganizerAPI(staged, {
    restoreGate: gate.promise,
    restoreMomentID: momentOneID,
    restoreAsCover: true,
  });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.click(
    await screen.findByRole("button", { name: "Restore removed Media" }),
  );
  expect(
    await screen.findByRole("button", { name: "Restoring…" }),
  ).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: "Merged during restoration" },
  });
  fireEvent.click(
    screen.getAllByRole("button", { name: "Merge with previous Moment" })[1],
  );
  expect(screen.getAllByLabelText(/^Title for Moment/)).toHaveLength(1);

  act(() => gate.resolve());

  const unassigned = screen
    .getByRole("heading", { name: "Unassigned Media" })
    .closest("section")!;
  await waitFor(() =>
    expect(
      within(unassigned).getByRole("checkbox", { name: /loose photo/ }),
    ).toBeInTheDocument(),
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Restored Media was moved to Unassigned Media because its original Moment was removed while restoration was pending. Choose it in Unassigned Media, move it to a Moment, then review the Event before Publication.",
  );
  const momentTitles = screen.getAllByLabelText(/^Title for Moment/);
  expect(momentTitles).toHaveLength(1);
  expect(momentTitles[0]).toHaveValue("Saturday");
  expect(screen.getByLabelText("Event title")).toHaveValue(
    "Merged during restoration",
  );
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    version: 9,
    title: "Merged during restoration",
    unassigned_media_ids: [items.loose.id],
  });
  expect(saves[0].moments).toHaveLength(1);
  expect(saves[0].moments[0]).toMatchObject({
    id: momentTwoID,
    media_item_ids: [items.b.id, items.c.id, items.a.id],
    place_labels: ["Harbor overlook", "Shared table", "Garden terrace"],
  });
});

test("rebases review completed while published Media restoration is pending", async () => {
  const staged = organizedDraft(8);
  staged.lifecycle = "published";
  staged.published_editable_version = 7;
  staged.moments[0].attendance_complete = true;
  staged.moments[1].cover_media_item_id = items.a.id;
  staged.moments[1].media_items = staged.moments[1].media_items.filter(
    (item) => item.id !== items.loose.id,
  );
  staged.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "removal",
        count: 1,
        media_item_ids: [items.loose.id],
        moment_ids: [momentOneID],
        removed_media: [
          {
            id: items.loose.id,
            media_type: items.loose.media_type,
            local_date_time: items.loose.local_date_time,
            restorable: true,
          },
        ],
        detail: "Media removed",
      },
    ],
  };
  const gate = deferred();
  stubOrganizerAPI(staged, {
    restoreGate: gate.promise,
    restoreMomentID: momentOneID,
    restoreAsCover: true,
  });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(
    (
      await screen.findAllByRole("button", {
        name: "Inspect Attendance and Audience",
      })
    )[1],
  );
  const confirmAttendance = await screen.findByRole("button", {
    name: "Confirm Attendance",
  });

  fireEvent.click(
    screen.getByRole("button", { name: "Restore removed Media" }),
  );
  fireEvent.click(confirmAttendance);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Approve Curator only" }),
    ).toBeEnabled(),
  );
  act(() => gate.resolve());

  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: /loose photo/ }),
    ).toBeInTheDocument(),
  );
  const readiness = screen
    .getByRole("heading", { name: "Readiness" })
    .closest("section")!;
  expect(
    within(readiness).getByText("Attendance").closest("li"),
  ).toHaveTextContent("✓ Attendance");
  expect(screen.getByText("All changes saved")).toBeInTheDocument();
});

test("refreshes Publication state while preserving edits made during refetch", async () => {
  const ready = organizedDraft(8);
  ready.lifecycle = "published";
  ready.published_editable_version = 7;
  ready.final_review_complete = true;
  ready.moments = ready.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  ready.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "metadata",
        count: 1,
        media_item_ids: [],
        moment_ids: [],
        event_metadata_fields: ["title"],
        detail: "Event metadata changed",
      },
    ],
  };
  ready.withdrawals = [
    {
      id: "77777777-7777-4777-8777-777777777777",
      target_kind: "media",
      target_id: items.a.id,
      reason: "Privacy request",
      withdrawn_by_name: "Robin",
      withdrawn_at: "2026-05-03T00:00:00Z",
      restored_by_publication_id: null,
      restored_at: null,
      affected_recipient_count: 1,
      affected_media_count: 1,
      affected_event_count: 1,
    },
  ];
  const gate = deferred();
  const saves = stubOrganizerAPI(ready, {
    publicationRefetchGate: gate.promise,
  });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const recipientSelect = await screen.findByLabelText("Preview Recipient");
  await waitFor(() => expect(recipientSelect).toBeEnabled());
  fireEvent.change(recipientSelect, {
    target: { value: "ffffffff-ffff-4fff-8fff-ffffffffffff" },
  });
  const previewButton = screen.getByRole("button", {
    name: "Preview as Recipient",
  });
  await waitFor(() => expect(previewButton).toBeEnabled());
  fireEvent.click(previewButton);
  expect(
    await screen.findByText("1 authorized Media items"),
  ).toBeInTheDocument();

  fireEvent.click(await screen.findByRole("button", { name: "Publish Event" }));
  expect(
    await screen.findByRole("button", { name: "Publishing…" }),
  ).toBeDisabled();
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Preview as Recipient" }),
  ).toBeDisabled();
  const title = screen.getByLabelText("Event title");
  fireEvent.change(title, { target: { value: "Follow-up correction" } });
  const eventLabels = screen.getByLabelText("Event Place labels");
  fireEvent.change(eventLabels, {
    target: { value: "Publication Event Place" },
  });
  fireEvent.blur(eventLabels);
  const momentLabels = screen.getByLabelText("Place labels for Moment 1");
  fireEvent.change(momentLabels, {
    target: { value: "Publication Moment Place" },
  });
  fireEvent.blur(momentLabels);
  act(() => gate.resolve());

  const history = await screen.findByText(/Privacy request by Robin/);
  expect(history).toHaveTextContent("Restored by a later Publication.");
  expect(title).toHaveValue("Follow-up correction");
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    version: 8,
    title: "Follow-up correction",
    place_labels: ["Publication Event Place"],
  });
  expect(saves[0].moments[0].place_labels).toEqual([
    "Publication Moment Place",
  ]);
  expect(history).toBeVisible();
});

test("publishes ready work and previews Recipient output read only", async () => {
  const ready = organizedDraft();
  ready.final_review_complete = true;
  ready.moments = ready.moments.map((moment) => ({
    ...moment,
    cover_media_item_id: null,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(ready);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: /Family weekend/ },
      contentionWait,
    ),
  );

  const publish = await screen.findByRole("button", { name: "Publish Event" });
  expect(screen.getByText("6 of 6 complete")).toBeInTheDocument();
  const readiness = screen
    .getByRole("heading", { name: "Readiness" })
    .closest("section")!;
  expect(readiness).toHaveTextContent("Next action: Ready to publish");
  expect(publish).toBeEnabled();
  fireEvent.click(publish);
  expect(
    await screen.findByText("Published revision 1 atomically."),
  ).toBeInTheDocument();
  expect(publish).toBeDisabled();
  await waitFor(() =>
    expect(readiness).toHaveTextContent(
      "Publication status: Published and up to date",
    ),
  );
  expect(readiness).not.toHaveTextContent("Next action: Ready to publish");

  const recipient = await screen.findByLabelText("Preview Recipient");
  fireEvent.change(recipient, {
    target: { value: "ffffffff-ffff-4fff-8fff-ffffffffffff" },
  });
  expect(
    screen.getByText(
      "Pending Recipient: cannot access yet. Preview shows approved content after Onboarding.",
    ),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Preview as Recipient" }));
  const preview = await screen.findByRole("region", {
    name: "Read-only Recipient preview",
  });
  expect(
    await screen.findByText("1 authorized Media items"),
  ).toBeInTheDocument();
  expect(preview).toHaveTextContent(
    "Preview activity is not recorded as Recipient engagement.",
  );
  for (const action of ["Comment", "Favorite", "Settings", "Download"]) {
    expect(
      screen.getByRole("button", { name: action, hidden: true }),
    ).toBeDisabled();
  }
  fireEvent.change(recipient, {
    target: { value: "abababab-abab-4bab-8bab-abababababab" },
  });
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
  expect(recipient).toHaveValue("abababab-abab-4bab-8bab-abababababab");
  fireEvent.change(recipient, {
    target: { value: "ffffffff-ffff-4fff-8fff-ffffffffffff" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview as Recipient" }));
  await screen.findByRole("region", { name: "Read-only Recipient preview" });
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "A new correction" },
  });
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("Preview Recipient")).toHaveValue(
    "ffffffff-ffff-4fff-8fff-ffffffffffff",
  );
  expect(readiness).toHaveTextContent("Next action: Ready to publish");
  expect(readiness).not.toHaveTextContent("Published and up to date");
});

test("enables only the server-authorized no-Staged Withdrawal Publication", async () => {
  const pending = organizedDraft(2);
  pending.lifecycle = "published";
  pending.published_editable_version = 1;
  pending.pending_withdrawal_publication = true;
  pending.final_review_complete = true;
  pending.moments = pending.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(pending);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const readiness = (
    await screen.findByRole("heading", { name: "Readiness" })
  ).closest("section")!;
  expect(readiness).toHaveTextContent(
    "Next action: Publish pending Withdrawal restoration",
  );
  expect(readiness).not.toHaveTextContent("Published and up to date");
  expect(screen.getByRole("button", { name: "Publish Event" })).toBeEnabled();
});

test("keeps a nonpending published Event with no Staged update disabled", async () => {
  const current = organizedDraft(2);
  current.lifecycle = "published";
  current.published_editable_version = 1;
  current.final_review_complete = true;
  current.moments = current.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(current);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const readiness = (
    await screen.findByRole("heading", { name: "Readiness" })
  ).closest("section")!;
  expect(readiness).toHaveTextContent(
    "Publication status: Published and up to date",
  );
  expect(readiness).not.toHaveTextContent("pending Withdrawal restoration");
  expect(screen.getByRole("button", { name: "Publish Event" })).toBeDisabled();
});

test("does not call an invalid unsaved correction published and up to date", async () => {
  const published = organizedDraft();
  published.lifecycle = "published";
  published.published_editable_version = published.version;
  published.final_review_complete = true;
  published.moments = published.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(published);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const readiness = (
    await screen.findByRole("heading", { name: "Readiness" })
  ).closest("section")!;
  expect(readiness).toHaveTextContent(
    "Publication status: Published and up to date",
  );

  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: " " },
  });

  expect(screen.getByText("Event title is required.")).toBeInTheDocument();
  expect(
    screen.getByText("Fix validation errors before autosave"),
  ).toBeVisible();
  expect(readiness).toHaveTextContent("Next action: Event details");
  expect(readiness).not.toHaveTextContent("Published and up to date");
});

test("requires a replacement Publication when legacy Attendance cannot be reconstructed", async () => {
  const recovery = organizedDraft(2);
  recovery.lifecycle = "published";
  recovery.published_editable_version = 1;
  recovery.published_attendance_recovery_required = true;
  stubOrganizerAPI(recovery);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Person search is unavailable for this existing Publication because its Attendance cannot be reconstructed safely. Review and publish the Event again to restore it.",
  );
});

test("withdraws a currently published target even when the staged draft differs", async () => {
  const published = organizedDraft();
  published.lifecycle = "published";
  published.final_review_complete = true;
  published.published_editable_version = published.version;
  published.moments = published.moments
    .filter((moment) => moment.id !== momentOneID)
    .map((moment) => ({
      ...moment,
      attendance_complete: true,
      audience_complete: true,
      media_items: [...moment.media_items, items.loose],
    }));
  stubOrganizerAPI(published);
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const target = await screen.findByLabelText("Currently published target");
  expect(target).toHaveTextContent("Moment: Friday");
  expect(target).not.toHaveTextContent("loose photo");
  fireEvent.change(target, { target: { value: momentOneID } });
  expect(
    screen.getByText(/Reused Media may require several Publications/),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Attributable reason"), {
    target: { value: "Privacy request" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Withdraw access" }));

  expect(
    await screen.findByText(
      "Access withdrawn for 2 Recipients across 3 Media items. Withdrawal created no new external notification. A delivery already handed off before it committed may still arrive.",
    ),
  ).toBeInTheDocument();
  const history = await screen.findByText(/Privacy request by Robin/);
  expect(history).toHaveTextContent("moment: Privacy request");
  expect(history).toHaveTextContent("Access remains withdrawn.");
  expect(screen.getByRole("button", { name: "Publish Event" })).toBeDisabled();
  expect(confirm).toHaveBeenCalledWith(
    "Withdraw Recipient access immediately? Identity and history will be preserved.",
  );
});

test("closes Preview as soon as Withdrawal commits", async () => {
  const published = organizedDraft();
  published.lifecycle = "published";
  published.final_review_complete = true;
  published.published_editable_version = published.version;
  published.moments = published.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  const reloadGate = deferred();
  stubOrganizerAPI(published, { withdrawalRefetchGate: reloadGate.promise });
  vi.spyOn(window, "confirm").mockReturnValue(true);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const recipientSelect = await screen.findByLabelText("Preview Recipient");
  await waitFor(() => expect(recipientSelect).toBeEnabled());
  fireEvent.change(recipientSelect, {
    target: { value: "ffffffff-ffff-4fff-8fff-ffffffffffff" },
  });
  const previewButton = screen.getByRole("button", {
    name: "Preview as Recipient",
  });
  await waitFor(() => expect(previewButton).toBeEnabled());
  fireEvent.click(previewButton);
  expect(
    await screen.findByText("1 authorized Media items"),
  ).toBeInTheDocument();

  fireEvent.change(screen.getByLabelText("Attributable reason"), {
    target: { value: "Privacy request" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Withdraw access" }));

  expect(
    await screen.findByText(
      "Reload the authoritative Event before Preview, Withdrawal, or Publication can continue.",
    ),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByText("1 authorized Media items"),
  ).not.toBeInTheDocument();

  reloadGate.resolve();
  await waitFor(() =>
    expect(
      screen.queryByText(
        "Reload the authoritative Event before Preview, Withdrawal, or Publication can continue.",
      ),
    ).not.toBeInTheDocument(),
  );
});

test("blocks stale Preview after Withdrawal until Event authority reloads", async () => {
  const published = organizedDraft();
  published.lifecycle = "published";
  published.final_review_complete = true;
  published.published_editable_version = published.version;
  published.moments = published.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(published, { withdrawalRefetchFailures: 10 });
  vi.spyOn(window, "confirm").mockReturnValue(true);
  const { client } = renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const recipientSelect = await screen.findByLabelText("Preview Recipient");
  await waitFor(() => expect(recipientSelect).toBeEnabled());
  fireEvent.change(recipientSelect, {
    target: { value: "ffffffff-ffff-4fff-8fff-ffffffffffff" },
  });
  const previewButton = screen.getByRole("button", {
    name: "Preview as Recipient",
  });
  await waitFor(() => expect(previewButton).toBeEnabled());
  fireEvent.click(previewButton);
  const preview = await screen.findByRole("region", {
    name: "Read-only Recipient preview",
  });
  await waitFor(() =>
    expect(preview).toHaveTextContent("1 authorized Media items"),
  );

  fireEvent.change(screen.getByLabelText("Attributable reason"), {
    target: { value: "Privacy request" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Withdraw access" }));

  expect(
    await screen.findByText(
      "Reload the authoritative Event before Preview, Withdrawal, or Publication can continue.",
    ),
  ).toBeInTheDocument();
  expect(
    await screen.findAllByText("The Event reload is temporarily unavailable."),
  ).toHaveLength(2);
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByText("1 authorized Media items"),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Preview as Recipient" }),
  ).toBeDisabled();
  expect(
    client
      .getQueriesData({
        queryKey: eventKeys.recipientPreviews(csrfToken),
      })
      .every(([, data]) => data === undefined),
  ).toBe(true);
});

test("preserves raw Place-label editing and autosaves parsed labels on blur", async () => {
  const saves = stubOrganizerAPI(draft());
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const eventLabels = await screen.findByLabelText("Event Place labels");
  fireEvent.focus(eventLabels);
  fireEvent.change(eventLabels, { target: { value: "São" } });
  fireEvent.change(eventLabels, { target: { value: "São " } });
  expect(eventLabels).toHaveValue("São ");
  expect(saves).toHaveLength(0);
  fireEvent.change(eventLabels, {
    target: { value: " São Paulo, , Jardin Central, " },
  });
  expect(eventLabels).toHaveValue(" São Paulo, , Jardin Central, ");
  fireEvent.blur(eventLabels);

  const momentLabels = screen.getByLabelText("Place labels for Moment 1");
  fireEvent.focus(momentLabels);
  fireEvent.change(momentLabels, {
    target: { value: " Café terrace, , River Walk, " },
  });
  expect(momentLabels).toHaveValue(" Café terrace, , River Walk, ");
  fireEvent.blur(momentLabels);

  expect(
    screen.getAllByText(
      "Up to 20 comma-separated labels, 120 characters each. Labels become searchable after Publication.",
    ),
  ).toHaveLength(3);
  await waitFor(() => expect(saves.length).toBeGreaterThan(0), contentionWait);
  await waitFor(
    () => expect(screen.getByText("All changes saved")).toBeInTheDocument(),
    contentionWait,
  );
  expect(saves.at(-1)?.place_labels).toEqual(["São Paulo", "Jardin Central"]);
  expect(saves.at(-1)?.moments[0].place_labels).toEqual([
    "Café terrace",
    "River Walk",
  ]);
});

test("rejects Place-label count and length limits before autosave", async () => {
  const saves = stubOrganizerAPI(draft());
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const labels = await screen.findByLabelText("Event Place labels");
  const tooMany = Array.from(
    { length: 21 },
    (_, index) => `Place ${index + 1}`,
  ).join(", ");
  fireEvent.change(labels, { target: { value: tooMany } });
  fireEvent.blur(labels);
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Use no more than 20 Place labels.",
  );
  expect(saves).toHaveLength(0);

  fireEvent.focus(labels);
  fireEvent.change(labels, { target: { value: "é".repeat(121) } });
  fireEvent.blur(labels);
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Each Place label must be 120 characters or fewer.",
  );
  expect(saves).toHaveLength(0);

  fireEvent.focus(labels);
  fireEvent.change(labels, { target: { value: "é".repeat(120) } });
  fireEvent.blur(labels);
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0].place_labels).toEqual(["é".repeat(120)]);
});

test("merges and deduplicates Moment Place labels", async () => {
  const initial = draft();
  initial.moments[0].place_labels = ["Garden", "Café"];
  initial.moments[1].place_labels = ["garden", "River Walk"];
  const saves = stubOrganizerAPI(initial);

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  await screen.findByRole(
    "heading",
    { name: "Family weekend" },
    contentionWait,
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Merge with previous Moment" })[1],
  );

  await waitFor(() =>
    expect(screen.getByLabelText("Place labels for Moment 1")).toHaveValue(
      "Garden, Café, River Walk",
    ),
  );
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0].moments[0].place_labels).toEqual([
    "Garden",
    "Café",
    "River Walk",
  ]);
});

test("blocks a Moment merge that would exceed the Place-label limit", async () => {
  const initial = draft();
  initial.moments[0].place_labels = Array.from(
    { length: 20 },
    (_, index) => `Place ${index + 1}`,
  );
  initial.moments[1].place_labels = ["Overflow Place"];
  const saves = stubOrganizerAPI(initial);

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  await screen.findByRole(
    "heading",
    { name: "Family weekend" },
    contentionWait,
  );
  fireEvent.click(
    screen.getAllByRole("button", { name: "Merge with previous Moment" })[1],
  );

  expect(screen.getByRole("alert")).toHaveTextContent(
    "Use no more than 20 Place labels. Remove Place labels before merging these Moments.",
  );
  expect(screen.getAllByText(/Moment \d ·/)).toHaveLength(2);
  expect(saves).toHaveLength(0);
});

test("rebases organization edits made while Withdrawal is pending", async () => {
  const published = organizedDraft();
  published.lifecycle = "published";
  published.final_review_complete = true;
  published.published_editable_version = published.version;
  const gate = deferred();
  const saves = stubOrganizerAPI(published, { withdrawalGate: gate.promise });
  vi.spyOn(window, "confirm").mockReturnValue(true);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.change(await screen.findByLabelText("Attributable reason"), {
    target: { value: "Privacy request" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Withdraw access" }));
  expect(
    await screen.findByRole("button", { name: "Withdrawing…" }),
  ).toBeDisabled();
  const title = screen.getByLabelText("Event title");
  fireEvent.change(title, { target: { value: "Edited during Withdrawal" } });
  fireEvent.change(screen.getAllByLabelText("Cover")[1], {
    target: { value: items.a.id },
  });
  const eventLabels = screen.getByLabelText("Event Place labels");
  fireEvent.change(eventLabels, {
    target: { value: "Withdrawal Event Place" },
  });
  fireEvent.blur(eventLabels);
  const momentLabels = screen.getByLabelText("Place labels for Moment 2");
  fireEvent.change(momentLabels, {
    target: { value: "Withdrawal Moment Place" },
  });
  fireEvent.blur(momentLabels);

  act(() => gate.resolve());
  const history = await screen.findByText(/Privacy request by Robin/);
  expect(history).toHaveTextContent("Access remains withdrawn.");
  expect(title).toHaveValue("Edited during Withdrawal");
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    version: 2,
    title: "Edited during Withdrawal",
    place_labels: ["Withdrawal Event Place"],
  });
  const withdrawnMoment = saves[0].moments.find(
    (moment) => moment.id === momentOneID,
  );
  expect(withdrawnMoment?.cover_media_item_id).toBe(items.a.id);
  expect(withdrawnMoment?.place_labels).toEqual(["Withdrawal Moment Place"]);
});

test("organizes merged and split days with pointer and keyboard controls", async () => {
  const saves = stubOrganizerAPI(draft());

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: /Family weekend/ },
      contentionWait,
    ),
  );
  expect(
    await screen.findByRole(
      "heading",
      { name: "Family weekend" },
      contentionWait,
    ),
  ).toBeInTheDocument();
  expect(screen.getByText("2 of 6 complete")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Readiness" }).closest("section"),
  ).toHaveTextContent("Next action: Media organization");

  const mergeButtons = screen.getAllByRole("button", {
    name: "Merge with previous Moment",
  });
  fireEvent.click(mergeButtons[1]);
  expect(screen.getAllByText(/Moment \d ·/)).toHaveLength(1);

  fireEvent.click(screen.getByRole("checkbox", { name: /second photo/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "Split selected into new Moment" }),
  );
  const splitMomentLabels = screen.getAllByText(/Moment \d ·/);
  expect(splitMomentLabels).toHaveLength(2);
  for (const label of splitMomentLabels)
    expect(label).toHaveTextContent("2026-05-01");

  const thirdCheckbox = screen.getByRole("checkbox", { name: /third photo/ });
  thirdCheckbox.focus();
  expect(thirdCheckbox).toHaveFocus();
  fireEvent.keyDown(thirdCheckbox, { key: "ArrowUp", altKey: true });

  fireEvent.click(screen.getByRole("checkbox", { name: /loose photo/ }));
  fireEvent.change(screen.getByLabelText("Move selected to"), {
    target: { value: momentOneID },
  });
  fireEvent.click(screen.getByRole("button", { name: "Move selected Media" }));

  const covers = screen.getAllByLabelText("Cover");
  fireEvent.change(covers[0], { target: { value: items.loose.id } });
  fireEvent.change(covers[1], { target: { value: items.b.id } });
  fireEvent.click(
    screen.getByRole("button", { name: "Move Moment 2 earlier" }),
  );

  await waitFor(() => expect(saves.length).toBeGreaterThan(0), contentionWait);
  await waitFor(
    () => expect(screen.getByText("All changes saved")).toBeInTheDocument(),
    contentionWait,
  );
  const lastSave = saves.at(-1)!;
  expect(lastSave.unassigned_media_ids).toEqual([]);
  expect(lastSave.moments).toHaveLength(2);
  expect(lastSave.moments[0].media_item_ids).toEqual([items.b.id]);
  expect(lastSave.moments[0].cover_media_item_id).toBe(items.b.id);
  expect(lastSave.moments[1].id).toBe(momentOneID);
  expect(lastSave.moments[1].media_item_ids).toEqual([
    items.c.id,
    items.a.id,
    items.loose.id,
  ]);
  expect(lastSave.moments[1].cover_media_item_id).toBe(items.loose.id);
}, 15_000);

test("renders large Event Media collections in bounded batches", async () => {
  const initial = draft();
  initial.unassigned_media = Array.from({ length: 201 }, (_, index) =>
    media(
      `99999999-9999-4999-8999-${String(index).padStart(12, "0")}`,
      `bulk photo ${index}`,
    ),
  );
  stubOrganizerAPI(initial);
  renderOrganizer(`/?event=${eventID}`);

  await waitFor(() =>
    expect(
      screen.getAllByRole("checkbox", { name: /bulk photo/ }),
    ).toHaveLength(200),
  );
  expect(
    screen.getByText("Showing 200 of 201 unassigned Media items."),
  ).toBeVisible();
  const first = screen.getByRole("checkbox", { name: /bulk photo 0$/ });
  const second = screen.getByRole("checkbox", { name: /bulk photo 1$/ });
  fireEvent.click(second);
  const afterSelection = screen.getAllByRole("checkbox", {
    name: /bulk photo/,
  });
  expect(afterSelection[0]).toBe(first);
  expect(afterSelection[1]).toBe(second);
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Unassigned Media" }),
  );
  expect(screen.getAllByRole("checkbox", { name: /bulk photo/ })).toHaveLength(
    201,
  );
});

test("keeps Source metadata advisory until the Curator explicitly accepts it", async () => {
  const initial = draft();
  initial.sources = [
    {
      id: "99999999-9999-4999-8999-999999999999",
      metadata_suggestion: {
        name: "Renamed Source album",
        description: "A newer Source description",
      },
    },
  ];
  const saves = stubOrganizerAPI(initial);
  renderOrganizer(`/?event=${eventID}`);

  expect(await screen.findByText("Source metadata suggestions")).toBeVisible();
  expect(screen.getByLabelText("Event title")).toHaveValue("Family weekend");
  expect(screen.getByLabelText("Event description")).toHaveValue("");
  expect(saves).toHaveLength(0);

  fireEvent.click(
    screen.getByRole("button", {
      name: "Use suggested title Renamed Source album",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "Use suggested description from Source",
    }),
  );

  expect(screen.getByLabelText("Event title")).toHaveValue(
    "Renamed Source album",
  );
  expect(screen.getByLabelText("Event description")).toHaveValue(
    "A newer Source description",
  );
  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0]).toMatchObject({
    title: "Renamed Source album",
    description: "A newer Source description",
  });
  expect(
    screen.getByRole("button", { name: "Suggested title currently used" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", {
      name: "Suggested description currently used",
    }),
  ).toBeDisabled();
});

test("can explicitly accept a Source description cleared to empty", async () => {
  const initial = draft();
  initial.description = "Portal-owned description";
  initial.sources = [
    {
      id: "99999999-9999-4999-8999-999999999999",
      metadata_suggestion: { name: null, description: "" },
    },
  ];
  const saves = stubOrganizerAPI(initial);
  renderOrganizer(`/?event=${eventID}`);

  expect(
    await screen.findByText("Suggested description: (empty description)"),
  ).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /Use suggested title/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Use suggested description from Source",
    }),
  );

  await waitFor(() => expect(saves).toHaveLength(1), contentionWait);
  expect(saves[0].description).toBe("");
});

test("autosaves Curator Event metadata and Media removal corrections", async () => {
  const initial = organizedDraft();
  const saves = stubOrganizerAPI(initial);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  fireEvent.change(await screen.findByLabelText("Event title"), {
    target: { value: "Corrected family weekend" },
  });
  fireEvent.change(screen.getByLabelText("Event description"), {
    target: { value: "A corrected description" },
  });
  fireEvent.change(screen.getByLabelText("Grouping timezone"), {
    target: { value: "America/New_York" },
  });
  fireEvent.click(screen.getByRole("checkbox", { name: /second photo/ }));
  fireEvent.click(
    screen.getByRole("button", { name: "Remove selected Media" }),
  );

  await waitFor(() => expect(saves.length).toBeGreaterThan(0), contentionWait);
  await screen.findByText("All changes saved", {}, contentionWait);
  const saved = saves.at(-1)!;
  expect(saved.title).toBe("Corrected family weekend");
  expect(saved.description).toBe("A corrected description");
  expect(saved.grouping_timezone).toBe("America/New_York");
  expect(
    saved.moments.flatMap((moment) => moment.media_item_ids),
  ).not.toContain(items.b.id);
  expect(screen.queryByRole("checkbox", { name: /second photo/ })).toBeNull();
});

test("blocks invalid Event metadata until title and timezone are valid", async () => {
  const initial = organizedDraft();
  initial.lifecycle = "published";
  initial.published_editable_version = initial.version - 1;
  initial.final_review_complete = true;
  for (const moment of initial.moments) {
    moment.attendance_complete = true;
    moment.audience_complete = true;
  }
  initial.staged_update = {
    id: "12121212-1212-4212-8212-121212121212",
    base_publication_id: "13131313-1313-4313-8313-131313131313",
    updated_at: "2026-05-03T01:00:00Z",
    changes: [
      {
        kind: "metadata",
        count: 1,
        media_item_ids: [],
        moment_ids: [],
        event_metadata_fields: ["description"],
        detail: "Event metadata edited",
      },
    ],
  };
  const saves = stubOrganizerAPI(initial);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const title = await screen.findByLabelText("Event title");
  const timezone = screen.getByLabelText("Grouping timezone");
  expect(title).toBeRequired();
  expect(timezone).toBeRequired();

  vi.useFakeTimers();
  fireEvent.change(title, { target: { value: "   " } });
  fireEvent.change(timezone, { target: { value: "Mars/Olympus" } });

  expect(title).toHaveAttribute("aria-invalid", "true");
  expect(timezone).toHaveAttribute("aria-invalid", "true");
  expect(screen.getByText("Event title is required.")).toBeInTheDocument();
  expect(
    screen.getByText(
      "Enter a valid IANA timezone, such as America/New_York or UTC.",
    ),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Fix validation errors before autosave"),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("region", {
      name: "Event details not ready to publish",
    }),
  ).toHaveTextContent(
    "Fix the Event detail validation errors before this review can be saved or published.",
  );
  expect(screen.getByText("5 of 6 complete")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Readiness" }).closest("section"),
  ).toHaveTextContent("Next action: Event details");

  await act(async () => vi.advanceTimersByTimeAsync(500));
  expect(saves).toHaveLength(0);

  fireEvent.change(title, { target: { value: "Valid corrected title" } });
  await act(async () => vi.advanceTimersByTimeAsync(500));
  expect(saves).toHaveLength(0);

  fireEvent.change(timezone, { target: { value: "America/New_York" } });
  await act(async () => vi.advanceTimersByTimeAsync(500));
  expect(saves).toHaveLength(1);
  vi.useRealTimers();
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
  expect(saves[0].title).toBe("Valid corrected title");
  expect(saves[0].grouping_timezone).toBe("America/New_York");
});

test("autosaves readiness and persists the complete organization after reload", async () => {
  const saves = stubOrganizerAPI(organizedDraft());

  const firstRender = renderOrganizer();
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: /Family weekend/ },
      contentionWait,
    ),
  );
  expect(
    await screen.findByRole(
      "heading",
      { name: "Family weekend" },
      contentionWait,
    ),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Inspect Attendance and Audience",
    })[0],
  );
  expect(
    screen.getByRole("button", { name: "Inspect", pressed: true }),
  ).toBeInTheDocument();
  expect(document.querySelector(".inspect-pane")).toHaveFocus();
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  );
  const firstApproval = await screen.findByRole("button", {
    name: "Approve Curator only",
  });
  await waitFor(() => expect(firstApproval).toBeEnabled());
  fireEvent.click(firstApproval);
  await screen.findByText(/Approved snapshot:/);
  fireEvent.click(screen.getByRole("button", { name: "Event" }));
  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Inspect Attendance and Audience",
    })[1],
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  );
  const secondApproval = await screen.findByRole("button", {
    name: "Approve Curator only",
  });
  await waitFor(() => expect(secondApproval).toBeEnabled());
  fireEvent.click(secondApproval);
  await screen.findByText(/Approved snapshot:/);
  fireEvent.click(screen.getByLabelText("Final review complete"));

  expect(screen.getByText("6 of 6 complete")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Readiness" }).closest("section"),
  ).toHaveTextContent("Next action: Ready to publish");

  await waitFor(() => expect(saves.length).toBeGreaterThan(0), contentionWait);
  await waitFor(
    () => expect(screen.getByText("All changes saved")).toBeInTheDocument(),
    contentionWait,
  );
  const lastSave = saves.at(-1)!;
  expect(lastSave.unassigned_media_ids).toEqual([]);
  expect(lastSave.moments).toHaveLength(2);
  expect(lastSave.moments[0].media_item_ids).toEqual([items.b.id]);
  expect(lastSave.moments[0].cover_media_item_id).toBe(items.b.id);
  expect(lastSave.moments[1].id).toBe(momentOneID);
  expect(lastSave.moments[1].media_item_ids).toEqual([
    items.c.id,
    items.a.id,
    items.loose.id,
  ]);
  expect(lastSave.moments[1].cover_media_item_id).toBe(items.loose.id);
  expect(lastSave.moments[0]).not.toHaveProperty("attendance_complete");
  expect(lastSave.moments[0]).not.toHaveProperty("audience_complete");
  expect(lastSave.final_review_complete).toBe(true);

  firstRender.unmount();
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole(
      "button",
      { name: /Family weekend/ },
      contentionWait,
    ),
  );
  expect(
    await screen.findByLabelText("Final review complete", {}, contentionWait),
  ).toBeChecked();
  expect(document.querySelector(".unassigned li")).toBeNull();
  expect(
    screen
      .getAllByLabelText("Cover")
      .map((cover) => (cover as HTMLSelectElement).value),
  ).toEqual([items.b.id, items.loose.id]);
}, 15_000);

test("rebases Audience version changes without discarding unsaved organization", async () => {
  const saves = stubOrganizerAPI(draft());
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  await screen.findByLabelText("Title for Moment 1");
  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Inspect Attendance and Audience",
    })[0],
  );
  const confirm = await screen.findByRole("button", {
    name: "Confirm Attendance",
  });
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "Unsaved organization survives" },
  });
  fireEvent.click(confirm);

  await waitFor(() => expect(saves.length).toBeGreaterThan(0));
  expect(saves.at(-1)?.version).toBe(2);
  expect(saves.at(-1)?.moments[0].title).toBe("Unsaved organization survives");
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Unsaved organization survives",
  );
});

test("keeps newer authoritative review state after a delayed autosave response", async () => {
  const attendanceGate = deferred();
  const organizationGate = deferred();
  const initial = draft();
  initial.moments[1].attendance_complete = true;
  stubOrganizerAPI(initial, {
    attendanceGate: attendanceGate.promise,
    organizationGate: organizationGate.promise,
  });
  const { client } = renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  await screen.findByLabelText("Title for Moment 1");
  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Inspect Attendance and Audience",
    })[0],
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "Saving while review completes" },
  });
  await screen.findByText("Saving…");

  act(() => attendanceGate.resolve());
  await screen.findByRole("button", { name: "Approve Curator only" });
  const readiness = screen
    .getByRole("heading", { name: "Readiness" })
    .closest("section")!;
  await waitFor(() =>
    expect(
      within(readiness).getByText("Attendance").closest("li"),
    ).toHaveTextContent("✓ Attendance"),
  );

  act(() => organizationGate.resolve());
  await screen.findByText("All changes saved");
  expect(
    within(readiness).getByText("Attendance").closest("li"),
  ).toHaveTextContent("✓ Attendance");
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Saving while review completes",
  );
  const cached = client.getQueryData<DraftEvent>(
    eventKeys.detail(csrfToken, eventID),
  );
  expect(cached?.title).toBe("Family weekend");
  expect(cached?.moments[0]).toMatchObject({
    title: "Saving while review completes",
    attendance_complete: true,
  });
});

test("adopts newer authoritative Event state after autosave completes", async () => {
  stubOrganizerAPI(draft());
  const { client } = renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const title = await screen.findByLabelText("Title for Moment 1");
  fireEvent.change(title, { target: { value: "Saved organization" } });
  await screen.findByText("All changes saved");

  const cached = client.getQueryData<DraftEvent>(
    eventKeys.detail(csrfToken, eventID),
  )!;
  act(() => {
    client.setQueryData(eventKeys.detail(csrfToken, eventID), {
      ...cached,
      version: cached.version + 1,
      moments: cached.moments.map((moment, index) =>
        index === 0
          ? { ...moment, title: "Newer authoritative organization" }
          : moment,
      ),
    });
  });

  await waitFor(() =>
    expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
      "Newer authoritative organization",
    ),
  );
});

test("stays saved when Audience review follows a completed autosave", async () => {
  stubOrganizerAPI(draft());
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const title = await screen.findByLabelText("Title for Moment 1");
  fireEvent.change(title, { target: { value: "Saved organization" } });
  await screen.findByText("All changes saved");

  const inspectButtons = screen.getAllByRole("button", {
    name: "Inspect Attendance and Audience",
  });
  fireEvent.click(inspectButtons[0]);
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  );

  await waitFor(() => expect(inspectButtons[1]).toBeEnabled());
  expect(screen.getByText("All changes saved")).toBeInTheDocument();
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Saved organization",
  );
});

test("mobile drill-down moves between Work, Event organization, and inspection", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/moments/${momentOneID}/attendance-audience`)
        return response(emptyReview(momentOneID));
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  expect(
    screen.getByRole("button", { name: "Work", pressed: true }),
  ).toHaveAttribute("aria-controls", "work-pane");
  expect(document.querySelector(".work-pane")).toHaveFocus();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  expect(
    screen.getByRole("button", { name: "Event", pressed: true }),
  ).toHaveAttribute("aria-controls", "organize-pane");
  expect(document.querySelector(".organize-pane")).toHaveFocus();
  fireEvent.click(
    (
      await screen.findAllByRole("button", {
        name: "Inspect Attendance and Audience",
      })
    )[0],
  );
  expect(
    screen.getByRole("button", { name: "Inspect", pressed: true }),
  ).toHaveAttribute("aria-controls", "inspect-pane");
  expect(document.querySelector(".inspect-pane")).toHaveFocus();
  expect(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  ).toBeInTheDocument();
});

test("recovers an ordinary failed autosave with an explicit retry", async () => {
  const attempts: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        attempts.push(request);
        if (attempts.length === 1)
          return response(
            problemResponse("Autosave is temporarily unavailable.", 503),
            503,
          );
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "Recovered title" },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Autosave is temporarily unavailable.",
  );
  await new Promise((resolve) => window.setTimeout(resolve, 600));
  expect(attempts).toHaveLength(1);
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "Latest recovered title" },
  });
  await new Promise((resolve) => window.setTimeout(resolve, 600));
  expect(attempts).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Retry autosave" }));
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
  expect(attempts).toHaveLength(2);
  expect(attempts[0].moments[0].title).toBe("Recovered title");
  expect(attempts[1].moments[0].title).toBe("Latest recovered title");
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Latest recovered title",
  );
});

test("keeps stale autosaves recoverable without silently overwriting newer work", async () => {
  let serverDraft = draft();
  let conflict = true;
  const putBodies: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: serverDraft.title,
              version: serverDraft.version,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: serverDraft.updated_at,
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(serverDraft);
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        putBodies.push(request);
        if (conflict) {
          conflict = false;
          serverDraft = { ...draft(2), title: "Newer server work" };
          return response(
            problemResponse("This Event changed in another browser.", 409),
            409,
          );
        }
        expect(request.version).toBe(2);
        serverDraft = eventFromRequest(request);
        return response(serverDraft);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "My local title" },
  });

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Your edits have not overwritten the newer version.",
  );
  expect(putBodies).toHaveLength(1);
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Replacing it will discard organization saved by the other browser.",
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "Replace newer version with my changes",
    }),
  );
  await waitFor(() => expect(putBodies).toHaveLength(2), { timeout: 3000 });
  expect(putBodies[1].moments[0].title).toBe("My local title");
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
});

test("keeps a conflict recoverable when loading the newer version fails", async () => {
  let serverDraft = draft();
  let failRefetch = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: serverDraft.title,
              version: serverDraft.version,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: serverDraft.updated_at,
            },
          ],
        });
      if (path === `/api/events/${eventID}`) {
        if (failRefetch)
          return response(
            problemResponse("Newer work is temporarily unavailable.", 503),
            503,
          );
        return response(serverDraft);
      }
      if (path === `/api/events/${eventID}/organization`) {
        serverDraft = draft(2);
        serverDraft.moments[0].title = "Newer Moment organization";
        failRefetch = true;
        return response(
          problemResponse("This Event changed in another browser.", 409),
          409,
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "My local title" },
  });
  const conflict = await screen.findByRole("alert");
  fireEvent.click(screen.getByRole("button", { name: "Load newer version" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Newer work is temporarily unavailable.",
  );
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "My local title",
  );
  expect(conflict).toBeInTheDocument();

  failRefetch = false;
  fireEvent.click(screen.getByRole("button", { name: "Load newer version" }));
  await waitFor(() =>
    expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
      "Newer Moment organization",
    ),
  );
  expect(screen.getByText("All changes saved")).toBeInTheDocument();
});

test("blocks edits while conflict replacement loads the newer version", async () => {
  let initialLoad = true;
  let resolveRefetch: ((value: Response) => void) | undefined;
  const putBodies: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) {
        if (initialLoad) {
          initialLoad = false;
          return response(draft());
        }
        return new Promise<Response>((resolve) => {
          resolveRefetch = resolve;
        });
      }
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        putBodies.push(request);
        if (putBodies.length === 1)
          return response(
            problemResponse("This Event changed in another browser.", 409),
            409,
          );
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "My local title" },
  });
  await screen.findByText("Save conflict");
  fireEvent.click(
    screen.getByRole("button", {
      name: "Replace newer version with my changes",
    }),
  );
  expect(screen.getByLabelText("Title for Moment 1")).toBeDisabled();
  expect(
    screen.getByRole("button", {
      name: "Replace newer version with my changes",
    }),
  ).toBeDisabled();

  resolveRefetch?.(response(draft(2)));
  await waitFor(() => expect(putBodies).toHaveLength(2), { timeout: 3000 });
  expect(putBodies[1].moments[0].title).toBe("My local title");
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
});

test("preserves edits made while an autosave is in flight", async () => {
  let resolveFirstSave: ((value: Response) => void) | undefined;
  const putBodies: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        putBodies.push(request);
        if (putBodies.length === 1)
          return new Promise<Response>((resolve) => {
            resolveFirstSave = resolve;
          });
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const title = await screen.findByLabelText("Title for Moment 1");
  fireEvent.change(title, { target: { value: "First edit" } });
  await waitFor(() => expect(putBodies).toHaveLength(1), { timeout: 3000 });
  fireEvent.change(title, { target: { value: "Later edit" } });
  await new Promise((resolve) => window.setTimeout(resolve, 600));
  expect(putBodies).toHaveLength(1);
  resolveFirstSave?.(response(eventFromRequest(putBodies[0])));

  await waitFor(() => expect(putBodies).toHaveLength(2), { timeout: 3000 });
  expect(putBodies[1].version).toBe(2);
  expect(putBodies[1].moments[0].title).toBe("Later edit");
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
});

test("creates the first Moment from selected undated Media", async () => {
  const undated = {
    ...draft(),
    moments: [],
    unassigned_media: [items.a, items.b],
  };
  const saves: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: undated.title,
              version: 1,
              moment_count: 0,
              unassigned_count: 2,
              updated_at: undated.updated_at,
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(undated);
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        saves.push(request);
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(await screen.findByRole("checkbox", { name: /first photo/ }));
  fireEvent.change(screen.getByLabelText("New Moment day"), {
    target: { value: "2026-05-04" },
  });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Create Moment from selected Media",
    }),
  );

  await waitFor(() => expect(saves).toHaveLength(1), { timeout: 3000 });
  expect(saves[0].moments).toHaveLength(1);
  expect(saves[0].moments[0].proposed_day).toBe("2026-05-04");
  expect(saves[0].moments[0].media_item_ids).toEqual([items.a.id]);
  expect(saves[0].unassigned_media_ids).toEqual([items.b.id]);
});

test("keeps bulk moves valid when Moments are emptied or merged", async () => {
  const saves: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        saves.push(request);
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Move selected to"), {
    target: { value: momentTwoID },
  });
  fireEvent.click(
    screen.getAllByRole("button", { name: "Merge with previous Moment" })[1],
  );
  expect(screen.getByLabelText("Move selected to")).toHaveValue(momentOneID);

  fireEvent.click(screen.getByRole("checkbox", { name: /loose photo/ }));
  fireEvent.click(screen.getByRole("button", { name: "Move selected Media" }));
  await waitFor(() => expect(saves).toHaveLength(1), { timeout: 3000 });
  expect(saves[0].moments).toHaveLength(1);
  expect(saves[0].moments[0].media_item_ids).toEqual([
    items.a.id,
    items.b.id,
    items.c.id,
    items.loose.id,
  ]);
  expect(saves[0].unassigned_media_ids).toEqual([]);
});

test("removes a Moment emptied by a bulk move", async () => {
  const saves: OrganizeEventRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${eventID}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        saves.push(request);
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(await screen.findByRole("checkbox", { name: /third photo/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: /second photo/ }));
  fireEvent.change(screen.getByLabelText("Move selected to"), {
    target: { value: momentOneID },
  });
  fireEvent.click(screen.getByRole("button", { name: "Move selected Media" }));

  await waitFor(() => expect(saves).toHaveLength(1), { timeout: 3000 });
  expect(saves[0].moments).toHaveLength(1);
  expect(saves[0].moments[0].media_item_ids).toEqual([
    items.a.id,
    items.b.id,
    items.c.id,
  ]);
});

test("loads the selected Event instead of retaining the previous draft", async () => {
  const secondID = "44444444-4444-4444-8444-444444444444";
  const first = draft();
  const second = { ...draft(), id: secondID, title: "Summer picnic" };
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: first.id,
              title: first.title,
              version: first.version,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: first.updated_at,
            },
            {
              id: second.id,
              title: second.title,
              version: second.version,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: second.updated_at,
            },
          ],
        });
      if (path === `/api/events/${first.id}`) return response(first);
      if (path === `/api/events/${second.id}`) return response(second);
      if (path === `/api/events/${first.id}/organization`) {
        const request = JSON.parse(
          stringBody(init?.body),
        ) as OrganizeEventRequest;
        return response(eventFromRequest(request));
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "Saved first Event" },
  });
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: /Summer picnic/ }));
  expect(
    await screen.findByRole("heading", { name: "Summer picnic" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue("Friday");
});

test("ignores a delayed Audience completion from a previously selected Event", async () => {
  const secondID = "44444444-4444-4444-8444-444444444444";
  const first = draft();
  const second = { ...draft(), id: secondID, title: "Summer picnic" };
  const attendanceGate = deferred();
  let attendanceCompleted = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: first.id,
              title: first.title,
              version: first.version,
              moment_count: first.moments.length,
              unassigned_count: first.unassigned_media.length,
              has_staged_update: false,
              lifecycle: first.lifecycle,
              updated_at: first.updated_at,
            },
            {
              id: second.id,
              title: second.title,
              version: second.version,
              moment_count: second.moments.length,
              unassigned_count: second.unassigned_media.length,
              has_staged_update: false,
              lifecycle: second.lifecycle,
              updated_at: second.updated_at,
            },
          ],
        });
      if (path === `/api/events/${first.id}`) return response(first);
      if (path === `/api/events/${second.id}`) return response(second);
      if (path === `/api/moments/${momentOneID}/attendance-audience`)
        return response(emptyReview(momentOneID));
      if (
        path === `/api/moments/${momentOneID}/attendance` &&
        init?.method === "PUT"
      )
        return attendanceGate.promise.then(() => {
          attendanceCompleted = true;
          const result = emptyReview(momentOneID);
          result.version = 2;
          result.attendance_confirmed = true;
          return response(result);
        });
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.click(
    (
      await screen.findAllByRole("button", {
        name: "Inspect Attendance and Audience",
      })
    )[0],
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Confirm Attendance" }),
  );
  fireEvent.click(screen.getByRole("button", { name: /Summer picnic/ }));
  await screen.findByRole("heading", { name: "Summer picnic" });
  fireEvent.change(screen.getByLabelText("Event title"), {
    target: { value: "" },
  });
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "Unsaved second Event" },
  });

  attendanceGate.resolve();
  await waitFor(() => expect(attendanceCompleted).toBe(true));
  expect(screen.getByRole("button", { name: /Summer picnic/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Unsaved second Event",
  );
});

test("opens a bookmarked Event in the Event pane", async () => {
  stubOrganizerAPI(draft());
  renderOrganizer(`/?event=${eventID}`);

  expect(
    await screen.findByRole("heading", { name: "Family weekend" }),
  ).toBeInTheDocument();
  await waitFor(() =>
    expect(document.getElementById("organize-pane")).toHaveFocus(),
  );
});

test("keeps transient organization state when URL navigation is rejected", async () => {
  const organizationGate = deferred();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  stubOrganizerAPI(draft(), { organizationGate: organizationGate.promise });
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  const mediaSelection = await screen.findByRole("checkbox", {
    name: /first photo/,
  });
  fireEvent.click(mediaSelection);
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "Unsaved URL navigation" },
  });
  await screen.findByText("Saving…");

  fireEvent.click(screen.getByRole("button", { name: "Navigate externally" }));

  await waitFor(() =>
    expect(
      screen.getByRole("heading", { name: "Family weekend" }),
    ).toBeVisible(),
  );
  expect(confirm).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Unsaved URL navigation",
  );
  expect(mediaSelection).toBeChecked();
  organizationGate.resolve();
});

test("does not silently discard a draft when navigating between Events", async () => {
  const secondID = "44444444-4444-4444-8444-444444444444";
  const second = { ...draft(), id: secondID, title: "Summer picnic" };
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
            {
              id: secondID,
              title: second.title,
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${secondID}`) return response(second);
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "Not saved yet" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Summer picnic/ }));
  expect(confirm).toHaveBeenCalledWith(
    "Discard changes that have not finished saving?",
  );
  expect(screen.getByRole("heading", { name: "Family weekend" })).toBeVisible();
  expect(screen.getByLabelText("Title for Moment 1")).toHaveValue(
    "Not saved yet",
  );
});

test("clears a failed autosave after confirmed Event navigation", async () => {
  const secondID = "44444444-4444-4444-8444-444444444444";
  const second = { ...draft(), id: secondID, title: "Summer picnic" };
  vi.spyOn(window, "confirm").mockReturnValue(true);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
            {
              id: secondID,
              title: second.title,
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) return response(draft());
      if (path === `/api/events/${secondID}`) return response(second);
      if (path === `/api/events/${eventID}/organization`)
        return response(
          problemResponse("Autosave is temporarily unavailable.", 503),
          503,
        );
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  fireEvent.change(await screen.findByLabelText("Title for Moment 1"), {
    target: { value: "Failed title" },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Autosave is temporarily unavailable.",
  );
  fireEvent.click(screen.getByRole("button", { name: /Summer picnic/ }));
  expect(
    await screen.findByRole("heading", { name: "Summer picnic" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

test("reports and retries a selected Event load failure", async () => {
  let failLoad = true;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathOf(input);
      if (path === "/api/events" && !init?.method)
        return response({
          events: [
            {
              id: eventID,
              title: "Family weekend",
              version: 1,
              moment_count: 2,
              unassigned_count: 1,
              updated_at: "2026-05-03T00:00:00Z",
            },
          ],
        });
      if (path === `/api/events/${eventID}`) {
        if (failLoad) {
          failLoad = false;
          return response(
            problemResponse("The Event is temporarily unavailable.", 503),
            503,
          );
        }
        return response(draft());
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The Event is temporarily unavailable.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry loading Event" }));
  expect(
    await screen.findByRole("heading", { name: "Family weekend" }),
  ).toBeInTheDocument();
});
