import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { EventOrganizer } from "./EventOrganizer";
import type {
  Event as DraftEvent,
  MediaItem,
  OrganizeEventRequest,
} from "./types/generated/events";

const csrfToken = "c".repeat(64);
const eventID = "11111111-1111-4111-8111-111111111111";
const momentOneID = "22222222-2222-4222-8222-222222222222";
const momentTwoID = "33333333-3333-4333-8333-333333333333";
const contentionWait = { timeout: 5_000 };

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
  const existing = draft(request.version + 1);
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

function stubOrganizerAPI(initial: DraftEvent) {
  let persisted = initial;
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
        persisted = {
          ...persisted,
          lifecycle: "published",
          published_editable_version: persisted.version,
          published_attendance_recovery_required: false,
        };
        return response(
          {
            id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
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
        const request = JSON.parse(stringBody(init.body)) as {
          target_kind: string;
          target_id: string;
          reason: string;
        };
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
        return response(persisted.withdrawals[0], 201);
      }
      if (path === `/api/events/${eventID}`) return response(persisted);
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
        return response(persisted);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  return saves;
}

function renderOrganizer() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <EventOrganizer
        session={{
          display_name: "Robin",
          session_type: "public",
          csrf_token: csrfToken,
          curator: true,
          onboarding_required: false,
        }}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("publishes ready work and previews Recipient output read only", async () => {
  const ready = organizedDraft();
  ready.final_review_complete = true;
  ready.moments = ready.moments.map((moment) => ({
    ...moment,
    attendance_complete: true,
    audience_complete: true,
  }));
  stubOrganizerAPI(ready);
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );

  const publish = await screen.findByRole("button", { name: "Publish Event" });
  expect(publish).toBeEnabled();
  fireEvent.click(publish);
  expect(
    await screen.findByText("Published revision 1 atomically."),
  ).toBeInTheDocument();
  expect(publish).toBeDisabled();

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
  fireEvent.change(screen.getByLabelText("Title for Moment 1"), {
    target: { value: "A new correction" },
  });
  expect(
    screen.queryByRole("region", { name: "Read-only Recipient preview" }),
  ).not.toBeInTheDocument();
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
  expect(screen.getByText("1 of 5 complete")).toBeInTheDocument();
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

  expect(screen.getByText("5 of 5 complete")).toBeInTheDocument();
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
            { error: { message: "Autosave is temporarily unavailable." } },
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
            { error: { message: "This Event changed in another browser." } },
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
            { error: { message: "Newer work is temporarily unavailable." } },
            503,
          );
        return response(serverDraft);
      }
      if (path === `/api/events/${eventID}/organization`) {
        serverDraft = draft(2);
        serverDraft.moments[0].title = "Newer Moment organization";
        failRefetch = true;
        return response(
          { error: { message: "This Event changed in another browser." } },
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
            { error: { message: "This Event changed in another browser." } },
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
          { error: { message: "Autosave is temporarily unavailable." } },
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
            { error: { message: "The Event is temporarily unavailable." } },
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
