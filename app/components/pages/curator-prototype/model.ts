// PROTOTYPE. In-memory album state and the projections the stubbed API serves.
// Access rules mirror the specification: an item decision wins over a Moment
// decision, which wins over an Album allow, then access is denied.
import type { Person } from "../../../types/generated/identity";
import type {
  AccessConflict,
  AccessPerson,
  Album,
  AlbumDetail,
  AudienceChange,
  Decision,
  Entry,
  FaceRecord,
  Moment,
  MomentAccess,
  StructurePreview,
} from "../../../types/generated/publishing";

export type Decisions = Record<string, Decision>;

export type StoredMoment = {
  id: string;
  title: string;
  cover_entry_id: string;
  decisions: Decisions;
  refreshed_at: string;
};

export type StoredEntry = {
  id: string;
  moment_id: string;
  filename: string;
  kind: "IMAGE" | "VIDEO";
  captured_at: string;
  available: boolean;
  thumbnail_url: string;
  width: number;
  height: number;
  src?: string;
  title?: string;
  chapters?: { title: string; time: number }[];
  faces: string[];
  decisions: Decisions;
};

export type StoredFace = {
  source_id: string;
  source_name: string;
  thumbnail_url: string;
  person_id: string;
  ignored: boolean;
};

export type Store = {
  curator: Person;
  people: Person[];
  album: Omit<
    Album,
    "photo_count" | "video_count" | "start_date" | "end_date" | "cover_url"
  >;
  albumAccess: Record<string, true>;
  moments: StoredMoment[];
  entries: StoredEntry[];
  faces: StoredFace[];
};

// Prototype-only projections that ticket 10 will add to the real API.
export type PrototypeAccessPerson = AccessPerson & {
  inherited: boolean;
  accessible_entries: number;
};
export type PrototypeEntry = Entry & { decisions: Decisions };
export type PrototypeMoment = Omit<Moment, "access" | "entries"> & {
  entries: PrototypeEntry[];
  access: Omit<MomentAccess, "people"> & { people: PrototypeAccessPerson[] };
};
export type AlbumPerson = {
  person_id: string;
  display_name: string;
  avatar_url: string;
  album_allowed: boolean;
  accessible_entries: number;
  moment_decisions: number;
  entry_decisions: number;
};
export type PrototypeAlbum = Omit<AlbumDetail, "moments"> & {
  moments: PrototypeMoment[];
  people: AlbumPerson[];
};
export type ViewerEntry = {
  id: string;
  kind: string;
  captured_at: string;
  thumbnail_url: string;
  full_url: string;
  download_url: string;
  filename: string;
  title: string;
  width: number;
  height: number;
  src: string;
  chapters: { title: string; time: number }[];
};
export type MemberAlbum = {
  id: string;
  title: string;
  description: string;
  cover_url: string;
  start_date: string;
  end_date: string;
  photo_count: number;
  video_count: number;
  published_at: string;
};
export type ViewerAlbum = {
  id: string;
  person_id: string;
  display_name: string;
  title: string;
  description: string;
  cover_url: string;
  start_date: string;
  end_date: string;
  photo_count: number;
  video_count: number;
  days: { date: string; entries: ViewerEntry[] }[];
};

export function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export function dateLabel(day: string) {
  return new Date(`${day}T00:00:00Z`).toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
  });
}

function day(entry: StoredEntry) {
  return entry.captured_at.slice(0, 10);
}

export function momentEntries(store: Store, momentID: string) {
  return store.entries
    .filter((entry) => entry.moment_id === momentID)
    .sort(
      (left, right) =>
        left.captured_at.localeCompare(right.captured_at) ||
        left.id.localeCompare(right.id),
    );
}

export function orderedMoments(store: Store) {
  const first = (moment: StoredMoment) => momentEntries(store, moment.id)[0];
  return [...store.moments].sort((left, right) => {
    const a = first(left)?.captured_at ?? "9999";
    const b = first(right)?.captured_at ?? "9999";
    return a.localeCompare(b) || left.id.localeCompare(right.id);
  });
}

// Effective access for one item and one person.
export function allowed(store: Store, entry: StoredEntry, personID: string) {
  const moment = store.moments.find((item) => item.id === entry.moment_id);
  const item = entry.decisions[personID];
  if (item === "allow" || item === "deny") return item === "allow";
  const scoped = moment?.decisions[personID];
  if (scoped === "allow" || scoped === "deny") return scoped === "allow";
  return !!store.albumAccess[personID];
}

export function accessibleEntries(store: Store, personID: string) {
  return store.entries.filter((entry) => allowed(store, entry, personID));
}

export function viewers(store: Store) {
  return store.people.filter(
    (person) => !person.deactivated_at && person.id !== store.curator.id,
  );
}

function personFaces(store: Store, personID: string) {
  return store.faces
    .filter((face) => face.person_id === personID)
    .map((face) => face.source_id);
}

export function supportingEntries(
  store: Store,
  momentID: string,
  personID: string,
) {
  const faces = personFaces(store, personID);
  return momentEntries(store, momentID).filter((entry) =>
    entry.faces.some((face) => faces.includes(face)),
  );
}

export function accessPeople(store: Store, moment: StoredMoment) {
  const entries = momentEntries(store, moment.id);
  return viewers(store).map((person): PrototypeAccessPerson => {
    const supporting = supportingEntries(store, moment.id, person.id).length;
    const decision = moment.decisions[person.id] ?? "";
    const inherited = decision === "" && !!store.albumAccess[person.id];
    return {
      person_id: person.id,
      display_name: person.display_name,
      avatar_url: person.avatar_url,
      decision: inherited ? "allow" : decision,
      inherited,
      detected: supporting > 0,
      suggested: supporting > 0 && decision === "" && !inherited,
      supporting_entries: supporting,
      accessible_entries: entries.filter((entry) =>
        allowed(store, entry, person.id),
      ).length,
    };
  });
}

export function suggestedPeople(store: Store, moment: StoredMoment) {
  return accessPeople(store, moment).filter((person) => person.suggested);
}

function faceRecords(store: Store, moment: StoredMoment): FaceRecord[] {
  const entries = momentEntries(store, moment.id);
  return store.faces
    .map((face) => {
      const occurrences = entries.filter((entry) =>
        entry.faces.includes(face.source_id),
      ).length;
      const person = store.people.find((item) => item.id === face.person_id);
      return {
        source_id: face.source_id,
        source_name: face.source_name,
        thumbnail_url: face.thumbnail_url,
        immich_url: `https://immich.example/people/${face.source_id}`,
        person_id: face.person_id,
        person_name: person?.display_name ?? "",
        ignored: face.ignored,
        occurrences,
      };
    })
    .filter((face) => face.occurrences > 0);
}

export function projectMoment(store: Store, moment: StoredMoment) {
  const entries = momentEntries(store, moment.id);
  const start = entries[0] ? day(entries[0]) : "";
  const end = entries.at(-1) ? day(entries.at(-1)!) : "";
  const projected: PrototypeMoment = {
    id: moment.id,
    title: moment.title,
    label: moment.title || dateLabel(start),
    date: start,
    end_date: end,
    cover_entry_id: moment.cover_entry_id,
    entries: entries.map((entry) => ({
      id: entry.id,
      media_id: `media-${entry.id}`,
      filename: entry.filename,
      kind: entry.kind,
      captured_at: entry.captured_at,
      available: entry.available,
      thumbnail_url: entry.thumbnail_url,
      decisions: entry.decisions,
    })),
    access: {
      people: accessPeople(store, moment),
      faces: faceRecords(store, moment),
      refreshed_at: moment.refreshed_at || undefined,
    },
  };
  return projected;
}

export function projectAlbum(store: Store): PrototypeAlbum {
  const photos = store.entries.filter((entry) => entry.kind === "IMAGE");
  const videos = store.entries.filter((entry) => entry.kind === "VIDEO");
  const days = store.entries.map(day).sort();
  const moments = orderedMoments(store).map((moment) =>
    projectMoment(store, moment),
  );
  const cover = moments
    .map((moment) =>
      moment.entries.find((entry) => entry.id === moment.cover_entry_id),
    )
    .find((entry) => entry?.available);
  return {
    ...store.album,
    photo_count: photos.length,
    video_count: videos.length,
    start_date: days[0] ?? "",
    end_date: days.at(-1) ?? "",
    cover_url: cover?.thumbnail_url ?? "",
    moments,
    people: viewers(store).map((person) => ({
      person_id: person.id,
      display_name: person.display_name,
      avatar_url: person.avatar_url,
      album_allowed: !!store.albumAccess[person.id],
      accessible_entries: accessibleEntries(store, person.id).length,
      moment_decisions: store.moments.filter((moment) =>
        ["allow", "deny"].includes(moment.decisions[person.id] ?? ""),
      ).length,
      entry_decisions: store.entries.filter((entry) =>
        ["allow", "deny"].includes(entry.decisions[person.id] ?? ""),
      ).length,
    })),
  };
}

export function projectAlbumSummary(store: Store): Album {
  const detail = projectAlbum(store);
  return {
    id: detail.id,
    source_id: detail.source_id,
    title: detail.title,
    description: detail.description,
    published: detail.published,
    status: detail.status,
    message: detail.message,
    processed: detail.processed,
    total: detail.total,
    photo_count: detail.photo_count,
    video_count: detail.video_count,
    start_date: detail.start_date,
    end_date: detail.end_date,
    cover_url: detail.cover_url,
  };
}

// The accessible Album cover follows ADR 0011: the earliest Moment whose
// configured cover the person can see, otherwise no cover at all.
export function viewerCover(store: Store, personID: string) {
  for (const moment of orderedMoments(store)) {
    const cover = store.entries.find(
      (entry) =>
        entry.id === moment.cover_entry_id && entry.moment_id === moment.id,
    );
    if (cover && allowed(store, cover, personID)) return cover;
  }
  return undefined;
}

export function projectViewer(store: Store, personID: string): ViewerAlbum {
  const person = store.people.find((item) => item.id === personID);
  const entries = accessibleEntries(store, personID).sort((left, right) =>
    left.captured_at.localeCompare(right.captured_at),
  );
  const days = [...new Set(entries.map(day))].sort();
  return {
    id: store.album.id,
    person_id: personID,
    display_name: person?.display_name ?? "",
    title: store.album.title,
    description: store.album.description,
    cover_url: viewerCover(store, personID)?.thumbnail_url ?? "",
    start_date: days[0] ?? "",
    end_date: days.at(-1) ?? "",
    photo_count: entries.filter((entry) => entry.kind === "IMAGE").length,
    video_count: entries.filter((entry) => entry.kind === "VIDEO").length,
    days: days.map((date) => ({
      date,
      entries: entries
        .filter((entry) => day(entry) === date)
        .map((entry) => viewerEntry(entry)),
    })),
  };
}

function viewerEntry(entry: StoredEntry): ViewerEntry {
  return {
    id: entry.id,
    kind: entry.kind,
    captured_at: entry.captured_at,
    thumbnail_url: entry.thumbnail_url,
    full_url: entry.thumbnail_url,
    download_url: entry.src ?? entry.thumbnail_url,
    filename: entry.filename,
    title: entry.title || entry.filename.replace(/\.[^.]+$/, ""),
    width: entry.width,
    height: entry.height,
    src: entry.src ?? "",
    chapters: entry.chapters ?? [],
  };
}

// The member-facing album list: one card per album the person can see.
export function projectMemberAlbums(store: Store, personID: string) {
  const viewer = projectViewer(store, personID);
  if (viewer.photo_count + viewer.video_count === 0) return [] as MemberAlbum[];
  const album: MemberAlbum = {
    id: store.album.id,
    title: viewer.title,
    description: viewer.description,
    cover_url: viewer.cover_url,
    start_date: viewer.start_date,
    end_date: viewer.end_date,
    photo_count: viewer.photo_count,
    video_count: viewer.video_count,
    published_at: "2025-09-08T18:30:00Z",
  };
  return [album];
}

export function audienceChanges(before: Store, after: Store): AudienceChange[] {
  return viewers(after)
    .map((person) => {
      const old = new Set(
        accessibleEntries(before, person.id).map((entry) => entry.id),
      );
      const current = new Set(
        accessibleEntries(after, person.id).map((entry) => entry.id),
      );
      return {
        person_id: person.id,
        display_name: person.display_name,
        gained_entry_ids: [...current].filter((id) => !old.has(id)),
        lost_entry_ids: [...old].filter((id) => !current.has(id)),
      };
    })
    .filter(
      (change) =>
        change.gained_entry_ids.length > 0 || change.lost_entry_ids.length > 0,
    );
}

export function preview(
  before: Store,
  after: Store,
  extra: Partial<StructurePreview> = {},
): StructurePreview {
  return {
    ready: true,
    review_token: "prototype-review",
    removes_moment: false,
    changes: audienceChanges(before, after),
    conflicts: [],
    ...extra,
  };
}

export function mergeConflicts(
  store: Store,
  source: StoredMoment,
  target: StoredMoment,
): AccessConflict[] {
  return viewers(store)
    .filter(
      (person) =>
        (source.decisions[person.id] ?? "inherit") !==
        (target.decisions[person.id] ?? "inherit"),
    )
    .map((person) => ({
      person_id: person.id,
      display_name: person.display_name,
      source: source.decisions[person.id] ?? "inherit",
      target: target.decisions[person.id] ?? "inherit",
    }));
}

export function earliest(store: Store, momentID: string) {
  return momentEntries(store, momentID)[0]?.id ?? "";
}
