import { album, photos, videos } from "../viewer-prototype/fixtures";

// Throwaway curation state. Face associations are fictional, not image analysis.
export const people = ["Jamie", "Alex", "Sam", "Taylor"];
export type Decision = "allow" | "deny" | "inherit";
export type Decisions = Record<string, Decision>;
export type Moment = {
  id: string;
  title: string;
  cover: string;
  decisions: Decisions;
};
export type Entry = {
  id: string;
  moment: string;
  day: string;
  image: string;
  label: string;
  kind: "photo" | "video";
  ratio: number;
  detected: string[];
  decisions: Decisions;
};
export type Study = {
  title: string;
  published: boolean;
  albumAccess: Decisions;
  moments: Moment[];
  entries: Entry[];
};
// Reuse generated illustrations at a realistic review size without adding private media.
export const studyPhotos = [
  ...photos,
  ...Array.from({ length: 88 }, (_, index) => ({
    ...photos[index % 12],
    id: `p${101 + index}`,
    alt: `Cabin photo ${index + 13}`,
  })),
].sort((a, b) => a.day.localeCompare(b.day) || a.id.localeCompare(b.id));

export const initialStudy: Study = {
  title: album.title,
  published: false,
  albumAccess: { Jamie: "allow" },
  moments: [
    {
      id: "arrival",
      title: "At the cabin",
      cover: "p07",
      decisions: { Alex: "allow" },
    },
    {
      id: "picnic",
      title: "Picnic & the lakeside walk",
      cover: "p13",
      decisions: { Alex: "allow", Sam: "allow", Taylor: "deny" },
    },
    { id: "home", title: "Before we left", cover: "p23", decisions: {} },
  ],
  entries: [
    ...studyPhotos.map((photo): Entry => ({
      id: photo.id,
      day: photo.day,
      image: photo.image,
      label: photo.alt,
      ratio: photo.ratio,
      kind: "photo",
      moment:
        photo.day === "2025-06-14"
          ? "arrival"
          : photo.day === "2025-06-15"
            ? "picnic"
            : "home",
      detected:
        photo.id === "p02"
          ? ["Taylor"]
          : photo.id === "p03"
            ? ["Jamie"]
            : photo.id === "p04"
              ? ["Sam"]
              : photo.id === "p14"
                ? ["Sam", "Taylor"]
                : [],
      decisions: photo.id === "p07" ? { Alex: "deny" } : {},
    })),
    ...videos.map((video): Entry => ({
      id: video.id,
      day: video.day,
      image: video.poster,
      label: video.title || video.filename.replace(/\.[^.]+$/, ""),
      ratio: 16 / 9,
      kind: "video",
      moment: "arrival",
      detected: [],
      decisions: {},
    })),
  ].sort((a, b) => a.day.localeCompare(b.day) || a.id.localeCompare(b.id)),
};

export function access(study: Study, entry: Entry, person: string) {
  const moment = study.moments.find((item) => item.id === entry.moment);
  for (const [decision, source] of [
    [entry.decisions[person], "This item"],
    [moment?.decisions[person], "Moment"],
    [study.albumAccess[person], "Album"],
  ]) {
    if (decision && decision !== "inherit")
      return { allowed: decision === "allow", source };
  }
  return { allowed: false, source: "No access decision" };
}

export function visibleEntries(study: Study, person: string) {
  return study.entries.filter((entry) => access(study, entry, person).allowed);
}

export function orderedMoments(study: Study) {
  const first = (moment: Moment) =>
    study.entries.find((entry) => entry.moment === moment.id);
  return [...study.moments].sort((a, b) => {
    const left = first(a);
    const right = first(b);
    return (
      (left?.day ?? "").localeCompare(right?.day ?? "") ||
      (left?.id ?? a.id).localeCompare(right?.id ?? b.id)
    );
  });
}

export function viewerCover(study: Study, person: string) {
  for (const moment of orderedMoments(study)) {
    const cover = study.entries.find(
      (entry) => entry.id === moment.cover && entry.moment === moment.id,
    );
    if (cover && access(study, cover, person).allowed) return cover;
  }
  return undefined;
}

export function detectedPeople(study: Study, moment: string) {
  return [
    ...new Set(
      study.entries
        .filter((entry) => entry.moment === moment)
        .flatMap((entry) => entry.detected),
    ),
  ];
}

export function pendingRecommendations(study: Study, moment: Moment) {
  return detectedPeople(study, moment.id).filter(
    (person) =>
      (!moment.decisions[person] || moment.decisions[person] === "inherit") &&
      study.albumAccess[person] !== "allow",
  );
}

export function accessDraft(study: Study, moment: Moment) {
  const draft = { ...moment.decisions };
  for (const person of pendingRecommendations(study, moment))
    draft[person] = "allow";
  return draft;
}

export function audienceChanges(before: Study, after: Study) {
  return people.map((person) => {
    const oldIds = visibleEntries(before, person).map((entry) => entry.id);
    const newIds = visibleEntries(after, person).map((entry) => entry.id);
    return {
      person,
      gained: newIds.filter((id) => !oldIds.includes(id)),
      lost: oldIds.filter((id) => !newIds.includes(id)),
    };
  });
}

export function dateRange(entries: Entry[]) {
  if (!entries.length) return "No accessible media";
  const format = (day: string) =>
    new Date(`${day}T12:00:00`).toLocaleDateString("en-US", {
      month: "long",
      day: "numeric",
      year: "numeric",
    });
  const dates = entries.map((entry) => entry.day).sort();
  return dates[0] === dates.at(-1)
    ? format(dates[0])
    : `${format(dates[0])} to ${format(dates.at(-1)!)}`;
}
