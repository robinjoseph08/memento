// PROTOTYPE. Replaces window.fetch with an in-memory API so the real app runs
// against fixtures. Covers today's Curator endpoints plus the ticket 10
// endpoints the prototype needs (Album access, rules, preview, publication).
import type { Person, PersonDetail } from "../../../types/generated/identity";
import type {
  Decision,
  MergeMomentsRequest,
  MoveEntriesRequest,
  SplitMomentRequest,
  UndoAccessChange,
} from "../../../types/generated/publishing";
import { initialStore } from "./fixtures";
import {
  accessPeople,
  clone,
  earliest,
  mergeConflicts,
  momentEntries,
  preview,
  projectAlbum,
  projectAlbumSummary,
  projectViewer,
  viewers,
  type Store,
  type StoredMoment,
} from "./model";

type Handler = (
  params: Record<string, string>,
  body: Record<string, unknown>,
  url: URL,
) => {
  status?: number;
  body?: unknown;
  message?: string;
  fields?: Record<string, string>;
};

let store = initialStore();
let signedOut = false;

function fail(
  message: string,
  fields: Record<string, string> = {},
  status = 400,
) {
  return { status, message, fields };
}

function moment(id: string) {
  const found = store.moments.find((item) => item.id === id);
  if (!found) throw new Error("Moment not found");
  return found;
}

function personDetail(id: string): PersonDetail {
  const person = store.people.find((item) => item.id === id);
  if (!person) throw new Error("Person not found");
  return {
    person,
    faces: store.faces
      .filter((face) => face.person_id === id)
      .map((face) => ({
        source_face_id: face.source_id,
        source_name: face.source_name,
        thumbnail_url: face.thumbnail_url,
        immich_url: `https://immich.example/people/${face.source_id}`,
        avatar: person.avatar_url === face.thumbnail_url,
      })),
    identities: [],
    preauthorizations: [],
    sessions: [],
  };
}

function setDecision(
  decisions: Record<string, Decision>,
  personID: string,
  decision: Decision | "",
) {
  if (decision === "allow" || decision === "deny")
    decisions[personID] = decision;
  else delete decisions[personID];
}

function undoChange(
  personID: string,
  current: Decision,
  previous: Decision,
): UndoAccessChange {
  return {
    person_id: personID,
    current,
    previous,
    current_updated_at: new Date().toISOString(),
  };
}

function removeMomentIfEmpty(id: string) {
  if (momentEntries(store, id).length === 0)
    store.moments = store.moments.filter((item) => item.id !== id);
}

function replaceLeavingCover(item: StoredMoment, leaving: string[]) {
  if (leaving.includes(item.cover_entry_id)) {
    const remaining = momentEntries(store, item.id).filter(
      (entry) => !leaving.includes(entry.id),
    );
    item.cover_entry_id = remaining[0]?.id ?? "";
  }
}

// Structural operations run against a copy first so previews and commits share
// one implementation.
function applyMove(base: Store, momentID: string, body: MoveEntriesRequest) {
  const next = clone(base);
  const source = next.moments.find((item) => item.id === momentID)!;
  const destination = next.moments.find(
    (item) => item.id === body.destination_moment_id,
  );
  if (!destination)
    return {
      error: fail("Choose a Moment.", {
        destination_moment_id: "Choose a destination Moment.",
      }),
    };
  if (!body.entry_ids?.length)
    return {
      error: fail("Select media.", { entry_ids: "Select at least one item." }),
    };
  const before = store;
  store = next;
  replaceLeavingCover(source, body.entry_ids);
  store = before;
  for (const entry of next.entries)
    if (body.entry_ids.includes(entry.id)) entry.moment_id = destination.id;
  const removes = !next.entries.some((entry) => entry.moment_id === source.id);
  if (removes)
    next.moments = next.moments.filter((item) => item.id !== source.id);
  return { next, removes };
}

function applySplit(base: Store, momentID: string, body: SplitMomentRequest) {
  const next = clone(base);
  const source = next.moments.find((item) => item.id === momentID)!;
  const remaining = next.entries.filter(
    (entry) =>
      entry.moment_id === momentID && !body.entry_ids.includes(entry.id),
  );
  if (!body.entry_ids?.length || remaining.length === 0)
    return {
      error: fail("Leave at least one item in this Moment.", {
        entry_ids: "Leave at least one item in this Moment.",
      }),
    };
  const id = `split-${Date.now()}`;
  for (const entry of next.entries)
    if (body.entry_ids.includes(entry.id)) entry.moment_id = id;
  if (body.entry_ids.includes(source.cover_entry_id))
    source.cover_entry_id = remaining[0].id;
  const before = store;
  store = next;
  next.moments.push({
    id,
    title: body.new_title ?? "",
    cover_entry_id: earliest(next, id),
    decisions: { ...source.decisions },
    refreshed_at: "",
  });
  store = before;
  return { next, removes: false };
}

function applyMerge(base: Store, momentID: string, body: MergeMomentsRequest) {
  const next = clone(base);
  const source = next.moments.find((item) => item.id === momentID)!;
  const target = next.moments.find((item) => item.id === body.target_moment_id);
  if (!target)
    return {
      error: fail("Choose a Moment.", {
        target_moment_id: "Choose a destination Moment.",
      }),
    };
  const conflicts = mergeConflicts(next, source, target).filter(
    (conflict) =>
      !body.resolutions?.some((item) => item.person_id === conflict.person_id),
  );
  if (!body.cover_entry_id)
    return {
      error: fail("Choose a cover.", {
        cover_entry_id: "Choose the merged Moment cover.",
      }),
    };
  for (const entry of next.entries)
    if (entry.moment_id === source.id) entry.moment_id = target.id;
  target.title = body.title || target.title;
  target.cover_entry_id = body.cover_entry_id;
  for (const resolution of body.resolutions ?? [])
    setDecision(target.decisions, resolution.person_id, resolution.decision);
  next.moments = next.moments.filter((item) => item.id !== source.id);
  return { next, removes: true, conflicts };
}

const routes: [string, string, Handler][] = [
  [
    "GET",
    "/api/identity/status",
    () => ({
      body: signedOut
        ? { claimed: true, auth_mode: "fake" }
        : { claimed: true, person: store.curator, auth_mode: "fake" },
    }),
  ],
  [
    "POST",
    "/api/identity/sign-out",
    () => {
      signedOut = true;
      return { body: {} };
    },
  ],
  [
    "POST",
    "/api/identity/sign-out-everywhere",
    () => {
      signedOut = true;
      return { body: {} };
    },
  ],
  [
    "POST",
    "/api/identity/fake-sign-in",
    () => {
      signedOut = false;
      return { body: store.curator };
    },
  ],
  [
    "GET",
    "/api/curator/connection",
    () => ({
      body: {
        usable: true,
        version: "v2.3.1 (prototype)",
        message: "Connected to the fixture library.",
        import_supported: true,
      },
    }),
  ],
  [
    "GET",
    "/api/setup/connection",
    () => ({
      body: {
        usable: true,
        version: "v2.3.1 (prototype)",
        message: "Connected to the fixture library.",
      },
    }),
  ],
  [
    "GET",
    "/api/curator/albums",
    (_params, _body, url) => {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const summary = projectAlbumSummary(store);
      return { body: summary.title.toLowerCase().includes(q) ? [summary] : [] };
    },
  ],
  [
    "GET",
    "/api/curator/sources",
    () => ({
      body: { albums: [], page: 1, pages: 1, total: 0 },
    }),
  ],
  ["GET", "/api/curator/albums/:album", () => ({ body: projectAlbum(store) })],
  [
    "POST",
    "/api/curator/albums/:album",
    (_params, body) => {
      const title = String(body.title ?? "").trim();
      if (!title)
        return fail("Check the highlighted fields.", {
          title: "Enter an album title.",
        });
      store.album.title = title;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/retry",
    () => ({ body: projectAlbum(store) }),
  ],
  [
    "POST",
    "/api/curator/albums/:album/reset",
    () => {
      store = initialStore();
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/publish",
    () => {
      if (!store.album.title.trim())
        return fail("Add an album title before publishing.");
      store.album.published = true;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/unpublish",
    () => {
      store.album.published = false;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "GET",
    "/api/curator/albums/:album/preview",
    (_params, _body, url) => {
      const person = url.searchParams.get("person") ?? "";
      if (!viewers(store).some((item) => item.id === person))
        return fail("Choose a person to preview as.", {}, 404);
      return { body: projectViewer(store, person) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/access",
    (_params, body) => {
      const personID = String(body.person_id);
      if (body.allowed) store.albumAccess[personID] = true;
      else delete store.albumAccess[personID];
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/access/preview",
    (_params, body) => {
      const next = clone(store);
      for (const item of (body.people ?? []) as {
        person_id: string;
        allowed: boolean;
      }[]) {
        if (item.allowed) next.albumAccess[item.person_id] = true;
        else delete next.albumAccess[item.person_id];
      }
      return { body: preview(store, next) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/access/save",
    (_params, body) => {
      for (const item of (body.people ?? []) as {
        person_id: string;
        allowed: boolean;
      }[]) {
        if (item.allowed) store.albumAccess[item.person_id] = true;
        else delete store.albumAccess[item.person_id];
      }
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/access/remove-all/preview",
    (_params, body) => {
      const next = clone(store);
      const personID = String(body.person_id);
      delete next.albumAccess[personID];
      for (const item of next.moments) delete item.decisions[personID];
      for (const entry of next.entries) delete entry.decisions[personID];
      return { body: preview(store, next) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/access/remove-all",
    (_params, body) => {
      const personID = String(body.person_id);
      delete store.albumAccess[personID];
      for (const item of store.moments) delete item.decisions[personID];
      for (const entry of store.entries) delete entry.decisions[personID];
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment",
    (params, body) => {
      moment(params.moment).title = String(body.title ?? "").trim();
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/cover",
    (params, body) => {
      const entryID = String(body.entry_id);
      if (
        !momentEntries(store, params.moment).some(
          (entry) => entry.id === entryID,
        )
      )
        return fail("Choose an item from this Moment.", {
          entry_id: "Choose an item from this Moment.",
        });
      moment(params.moment).cover_entry_id = entryID;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/faces/refresh",
    (params) => {
      moment(params.moment).refreshed_at = new Date().toISOString();
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/access",
    (params, body) => {
      const item = moment(params.moment);
      const personID = String(body.person_id);
      const decision = String(body.decision) as Decision;
      const previous = item.decisions[personID] ?? "";
      setDecision(item.decisions, personID, decision);
      return {
        body: {
          album: projectAlbum(store),
          undo: { changes: [undoChange(personID, decision, previous)] },
        },
      };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/access/suggestions",
    (params) => {
      const item = moment(params.moment);
      const changes = accessPeople(store, item)
        .filter((person) => person.suggested)
        .map((person) => {
          const previous = item.decisions[person.person_id] ?? "";
          item.decisions[person.person_id] = "allow";
          return undoChange(person.person_id, "allow", previous);
        });
      return { body: { album: projectAlbum(store), undo: { changes } } };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/access/undo",
    (params, body) => {
      const item = moment(params.moment);
      for (const change of (body.changes ?? []) as UndoAccessChange[])
        setDecision(item.decisions, change.person_id, change.previous);
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/rules",
    (params, body) => {
      const item = moment(params.moment);
      for (const rule of (body.decisions ?? []) as {
        person_id: string;
        decision: Decision;
      }[])
        setDecision(item.decisions, rule.person_id, rule.decision);
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/entries/:entry/rules",
    (params, body) => {
      const entry = store.entries.find((item) => item.id === params.entry);
      if (!entry) return fail("Item not found.", {}, 404);
      for (const rule of (body.decisions ?? []) as {
        person_id: string;
        decision: Decision;
      }[])
        setDecision(entry.decisions, rule.person_id, rule.decision);
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/move/preview",
    (params, body) => {
      const result = applyMove(
        store,
        params.moment,
        body as unknown as MoveEntriesRequest,
      );
      if (result.error) return result.error;
      return {
        body: preview(store, result.next, { removes_moment: result.removes }),
      };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/move",
    (params, body) => {
      const result = applyMove(
        store,
        params.moment,
        body as unknown as MoveEntriesRequest,
      );
      if (result.error) return result.error;
      store = result.next;
      removeMomentIfEmpty(params.moment);
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/split/preview",
    (params, body) => {
      const result = applySplit(
        store,
        params.moment,
        body as unknown as SplitMomentRequest,
      );
      if (result.error) return result.error;
      return { body: preview(store, result.next) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/split",
    (params, body) => {
      const result = applySplit(
        store,
        params.moment,
        body as unknown as SplitMomentRequest,
      );
      if (result.error) return result.error;
      store = result.next;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/merge/preview",
    (params, body) => {
      const result = applyMerge(
        store,
        params.moment,
        body as unknown as MergeMomentsRequest,
      );
      if (result.error) return result.error;
      if (result.conflicts.length)
        return {
          body: {
            ready: false,
            review_token: "",
            removes_moment: true,
            changes: [],
            conflicts: result.conflicts,
          },
        };
      return { body: preview(store, result.next, { removes_moment: true }) };
    },
  ],
  [
    "POST",
    "/api/curator/albums/:album/moments/:moment/merge",
    (params, body) => {
      const result = applyMerge(
        store,
        params.moment,
        body as unknown as MergeMomentsRequest,
      );
      if (result.error) return result.error;
      if (result.conflicts.length)
        return fail("Choose access for every listed person.", {
          resolutions: "Choose access for every listed person.",
        });
      store = result.next;
      return { body: projectAlbum(store) };
    },
  ],
  [
    "GET",
    "/api/people",
    (_params, _body, url) => {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      return {
        body: store.people.filter((person) =>
          person.display_name.toLowerCase().includes(q),
        ),
      };
    },
  ],
  [
    "POST",
    "/api/people",
    (_params, body) => {
      const display_name = String(body.display_name ?? "").trim();
      if (!display_name)
        return fail("Check the highlighted fields.", {
          display_name: "Enter a display name.",
        });
      const person: Person = {
        id: `person-${Date.now()}`,
        display_name,
        is_curator: false,
        update_email: "",
        email_updates: true,
        avatar_url: "",
      };
      store.people.push(person);
      return { body: person };
    },
  ],
  [
    "POST",
    "/api/people/from-face",
    (_params, body) => {
      const face = store.faces.find(
        (item) => item.source_id === body.source_face_id,
      );
      const display_name = String(body.display_name ?? "").trim();
      if (!face)
        return fail("Face not found.", {
          source_face_id: "This face is no longer available.",
        });
      if (!display_name)
        return fail("Check the highlighted fields.", {
          display_name: "Enter a display name.",
        });
      const person: Person = {
        id: `person-${Date.now()}`,
        display_name,
        is_curator: false,
        update_email: "",
        email_updates: true,
        avatar_url: face.thumbnail_url,
      };
      store.people.push(person);
      face.person_id = person.id;
      face.ignored = false;
      return { body: personDetail(person.id) };
    },
  ],
  [
    "GET",
    "/api/people/:person",
    (params) => ({ body: personDetail(params.person) }),
  ],
  [
    "POST",
    "/api/people/:person/faces",
    (params, body) => {
      const face = store.faces.find(
        (item) => item.source_id === body.source_face_id,
      );
      if (!face)
        return fail("Face not found.", {
          source_face_id: "This face is no longer available.",
        });
      face.person_id = params.person;
      face.ignored = false;
      return { body: personDetail(params.person) };
    },
  ],
  [
    "POST",
    "/api/faces/:face/ignore",
    (params) => {
      const face = store.faces.find((item) => item.source_id === params.face);
      if (face) face.ignored = true;
      return { body: {} };
    },
  ],
];

function match(method: string, pathname: string) {
  for (const [routeMethod, pattern, handler] of routes) {
    if (routeMethod !== method) continue;
    const expected = pattern.split("/");
    const actual = pathname.split("/");
    if (expected.length !== actual.length) continue;
    const params: Record<string, string> = {};
    const ok = expected.every((segment, index) => {
      if (segment.startsWith(":")) {
        params[segment.slice(1)] = decodeURIComponent(actual[index]);
        return true;
      }
      return segment === actual[index];
    });
    if (ok) return { handler, params };
  }
  return null;
}

// Every request waits a beat so pending states are visible while reviewing.
const latency = 160;

export function installPrototypeAPI() {
  const original = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = new URL(
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url,
      window.location.origin,
    );
    if (!url.pathname.startsWith("/api/")) return original(input, init);
    const method = (init?.method ?? "GET").toUpperCase();
    const found = match(method, url.pathname);
    await new Promise((resolve) => setTimeout(resolve, latency));
    if (!found)
      return new Response(
        JSON.stringify({
          error: {
            message: `No prototype handler for ${method} ${url.pathname}`,
          },
        }),
        { status: 404, headers: { "Content-Type": "application/json" } },
      );
    let body: Record<string, unknown> = {};
    if (typeof init?.body === "string" && init.body)
      body = JSON.parse(init.body);
    try {
      const result = found.handler(found.params, body, url);
      if (result.message)
        return new Response(
          JSON.stringify({
            error: { message: result.message, fields: result.fields ?? {} },
          }),
          {
            status: result.status ?? 400,
            headers: { "Content-Type": "application/json" },
          },
        );
      return new Response(JSON.stringify(result.body ?? {}), {
        status: result.status ?? 200,
        headers: { "Content-Type": "application/json" },
      });
    } catch (error) {
      return new Response(
        JSON.stringify({
          error: {
            message: error instanceof Error ? error.message : "Prototype error",
          },
        }),
        { status: 500, headers: { "Content-Type": "application/json" } },
      );
    }
  };
}
