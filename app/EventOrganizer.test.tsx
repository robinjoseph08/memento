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
    grouping_timezone: "UTC",
    version,
    final_review_complete: false,
    sources: [],
    moments: [
      {
        id: momentOneID,
        title: "Friday",
        proposed_day: "2026-05-01",
        grouping_timezone: "UTC",
        cover_media_item_id: items.a.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.a],
      },
      {
        id: momentTwoID,
        title: "Saturday",
        proposed_day: "2026-05-02",
        grouping_timezone: "UTC",
        cover_media_item_id: items.b.id,
        attendance_complete: false,
        audience_complete: false,
        media_items: [items.b, items.c],
      },
    ],
    unassigned_media: [items.loose],
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

function eventFromRequest(request: OrganizeEventRequest): DraftEvent {
  const byID = new Map(Object.values(items).map((item) => [item.id, item]));
  const existing = draft(request.version + 1);
  existing.final_review_complete = request.final_review_complete;
  existing.moments = request.moments.map((moment) => ({
    id: moment.id,
    title: moment.title ?? "",
    proposed_day: moment.proposed_day,
    grouping_timezone: "UTC",
    cover_media_item_id: moment.cover_media_item_id,
    attendance_complete: moment.attendance_complete,
    audience_complete: moment.audience_complete,
    media_items: moment.media_item_ids.map((id) => byID.get(id)!),
  }));
  existing.unassigned_media = request.unassigned_media_ids.map((id) =>
    byID.get(id)!,
  );
  return existing;
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
        }}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("organizes merged and split days with pointer and keyboard controls, autosaves, and persists after reload", async () => {
  let persisted = draft();
  const saves: OrganizeEventRequest[] = [];
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
      if (path === `/api/events/${eventID}`) return response(persisted);
      if (
        path === `/api/events/${eventID}/organization` &&
        init?.method === "PUT"
      ) {
        expect(init.headers).toMatchObject({ "X-Memento-CSRF": csrfToken });
        const request = JSON.parse(
          stringBody(init.body),
        ) as OrganizeEventRequest;
        saves.push(request);
        persisted = eventFromRequest(request);
        return response(persisted);
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  const firstRender = renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  expect(
    await screen.findByRole("heading", { name: "Family weekend" }),
  ).toBeInTheDocument();

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

  const thirdRow = screen
    .getByRole("checkbox", { name: /third photo/ })
    .closest("li");
  expect(thirdRow).not.toBeNull();
  fireEvent.keyDown(thirdRow!, { key: "ArrowUp", altKey: true });

  fireEvent.click(screen.getByRole("checkbox", { name: /loose photo/ }));
  fireEvent.change(screen.getByLabelText("Move selected to"), {
    target: { value: momentOneID },
  });
  fireEvent.click(screen.getByRole("button", { name: "Move selected Media" }));

  fireEvent.click(
    screen.getAllByRole("button", {
      name: "Inspect Attendance and Audience",
    })[0],
  );
  expect(
    screen.getByRole("button", { name: "Inspect", pressed: true }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByLabelText("Attendance reviewed"));
  fireEvent.click(screen.getByLabelText(/Audience reviewed/));
  fireEvent.click(screen.getByLabelText("Final review complete"));

  await waitFor(() => expect(saves.length).toBeGreaterThan(0), {
    timeout: 3000,
  });
  await waitFor(() =>
    expect(screen.getByText("All changes saved")).toBeInTheDocument(),
  );
  const lastSave = saves.at(-1)!;
  expect(lastSave.unassigned_media_ids).toEqual([]);
  expect(lastSave.moments).toHaveLength(2);
  expect(lastSave.moments[0].media_item_ids).toContain(items.loose.id);
  expect(lastSave.final_review_complete).toBe(true);

  firstRender.unmount();
  renderOrganizer();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  expect(await screen.findByLabelText("Final review complete")).toBeChecked();
  expect(document.querySelector(".unassigned li")).toBeNull();
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
      throw new Error(`Unexpected request: ${path}`);
    }),
  );

  renderOrganizer();
  expect(
    screen.getByRole("button", { name: "Work", pressed: true }),
  ).toBeInTheDocument();
  fireEvent.click(
    await screen.findByRole("button", { name: /Family weekend/ }),
  );
  expect(
    screen.getByRole("button", { name: "Event", pressed: true }),
  ).toBeInTheDocument();
  fireEvent.click(
    (
      await screen.findAllByRole("button", {
        name: "Inspect Attendance and Audience",
      })
    )[0],
  );
  expect(
    screen.getByRole("button", { name: "Inspect", pressed: true }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Attendance reviewed")).toBeInTheDocument();
});

test("recovers an ordinary failed autosave with an explicit retry", async () => {
  let attempts = 0;
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
        attempts += 1;
        if (attempts === 1)
          return response(
            { error: { message: "Autosave is temporarily unavailable." } },
            503,
          );
        return response(
          eventFromRequest(
            JSON.parse(stringBody(init?.body)) as OrganizeEventRequest,
          ),
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
    target: { value: "Recovered title" },
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Autosave is temporarily unavailable.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Retry autosave" }));
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
  expect(attempts).toBe(2);
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
  fireEvent.click(screen.getByRole("button", { name: "Keep my changes" }));
  await waitFor(() => expect(putBodies).toHaveLength(2), { timeout: 3000 });
  expect(putBodies[1].moments[0].title).toBe("My local title");
  expect(await screen.findByText("All changes saved")).toBeInTheDocument();
});
