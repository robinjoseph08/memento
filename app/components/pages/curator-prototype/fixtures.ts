// PROTOTYPE. Fictional people and a generated-illustration album sized like a
// real weekend: one big Moment of about a hundred items and two small ones.
import type { Person } from "../../../types/generated/identity";
import type { Store, StoredEntry } from "./model";

const media = (name: string) => `/prototype-media/${name}`;

function person(
  id: string,
  display_name: string,
  extra: Partial<Person> = {},
): Person {
  return {
    id,
    display_name,
    is_curator: false,
    update_email: `${id}@example.com`,
    email_updates: true,
    avatar_url: "",
    ...extra,
  };
}

const curator = person("morgan", "Morgan", { is_curator: true });

// The twelve landscape illustrations, reused to fill the cabin Moment.
const photoFiles = Array.from({ length: 25 }, (_, index) =>
  media(`photo-${String(index + 1).padStart(2, "0")}.jpg`),
);

function stamp(day: string, minutes: number) {
  const date = new Date(`${day}T08:00:00Z`);
  date.setUTCMinutes(date.getUTCMinutes() + minutes);
  return date.toISOString().replace(".000Z", "Z");
}

const facesByPhoto: Record<string, string[]> = {
  p02: ["face-taylor"],
  p03: ["face-jamie"],
  p04: ["face-sam"],
  p05: ["face-jamie", "face-alex"],
  p06: ["face-grandpa"],
  p08: ["face-grandpa"],
  p09: ["face-unnamed"],
  p10: ["face-jamie"],
  p12: ["face-grandpa"],
  p14: ["face-sam", "face-taylor"],
  p15: ["face-jamie"],
  p16: ["face-alex"],
  p17: ["face-jamie"],
  p18: ["face-sam"],
  p23: ["face-jamie"],
  p24: ["face-alex"],
  p101: ["face-alex"],
  p102: ["face-alex"],
  p103: ["face-alex", "face-jamie"],
  p104: ["face-alex"],
  p105: ["face-alex"],
  p106: ["face-sam"],
  p107: ["face-neighbor"],
  p111: ["face-unnamed"],
};

function photo(
  id: string,
  moment_id: string,
  day: string,
  minutes: number,
  file: string,
): StoredEntry {
  return {
    id,
    moment_id,
    filename: `IMG_${id.slice(1).padStart(4, "0")}.jpg`,
    kind: "IMAGE",
    captured_at: stamp(day, minutes),
    available: id !== "p110",
    thumbnail_url: file,
    faces: facesByPhoto[id] ?? [],
    decisions: id === "p07" ? { alex: "deny" } : {},
  };
}

const arrivalPhotos = [
  ...Array.from({ length: 12 }, (_, index) =>
    photo(
      `p${String(index + 1).padStart(2, "0")}`,
      "arrival",
      "2025-06-14",
      index * 9,
      photoFiles[index],
    ),
  ),
  ...Array.from({ length: 88 }, (_, index) =>
    photo(
      `p${101 + index}`,
      "arrival",
      "2025-06-14",
      120 + index * 4,
      photoFiles[index % 12],
    ),
  ),
];

const videos: StoredEntry[] = [
  {
    id: "v01",
    moment_id: "arrival",
    filename: "CLIP_0001.mp4",
    kind: "VIDEO",
    captured_at: stamp("2025-06-14", 95),
    available: true,
    thumbnail_url: media("video-01.jpg"),
    faces: ["face-jamie"],
    decisions: {},
  },
  {
    id: "v02",
    moment_id: "arrival",
    filename: "CLIP_0002.mp4",
    kind: "VIDEO",
    captured_at: stamp("2025-06-14", 300),
    available: true,
    thumbnail_url: media("video-02.jpg"),
    faces: [],
    decisions: {},
  },
];

const picnicPhotos = Array.from({ length: 10 }, (_, index) =>
  photo(
    `p${index + 13}`,
    "picnic",
    "2025-06-15",
    index * 25,
    photoFiles[index + 12],
  ),
);

const homePhotos = Array.from({ length: 3 }, (_, index) =>
  photo(
    `p${index + 23}`,
    "home",
    "2025-06-16",
    index * 40,
    photoFiles[index + 22],
  ),
);

export function initialStore(): Store {
  return {
    curator,
    people: [
      curator,
      person("jamie", "Jamie", { avatar_url: media("photo-03.jpg") }),
      person("alex", "Alex", { avatar_url: media("photo-16.jpg") }),
      person("sam", "Sam"),
      person("taylor", "Taylor"),
      person("priya", "Priya"),
      person("lee", "Lee", { deactivated_at: "2025-05-01T00:00:00Z" }),
    ],
    album: {
      id: "lake",
      source_id: "immich-album-lake",
      title: "A weekend by the lake",
      description:
        "Two nights at the cabin. Lakeside walks, a picnic in the shade, and a slow drive home.",
      published: false,
      status: "complete",
      message: "",
      processed: 115,
      total: 115,
    },
    albumAccess: { jamie: true },
    moments: [
      {
        id: "arrival",
        title: "At the cabin",
        cover_entry_id: "p07",
        decisions: { alex: "allow" },
        refreshed_at: "2025-09-10T16:20:00Z",
      },
      {
        id: "picnic",
        title: "Picnic & the lakeside walk",
        cover_entry_id: "p13",
        decisions: { alex: "allow", sam: "allow", taylor: "deny" },
        refreshed_at: "",
      },
      {
        id: "home",
        title: "",
        cover_entry_id: "p23",
        decisions: {},
        refreshed_at: "",
      },
    ],
    entries: [...arrivalPhotos, ...videos, ...picnicPhotos, ...homePhotos],
    faces: [
      {
        source_id: "face-jamie",
        source_name: "Jamie",
        thumbnail_url: media("photo-03.jpg"),
        person_id: "jamie",
        ignored: false,
      },
      {
        source_id: "face-alex",
        source_name: "Alex",
        thumbnail_url: media("photo-16.jpg"),
        person_id: "alex",
        ignored: false,
      },
      {
        source_id: "face-sam",
        source_name: "Sam",
        thumbnail_url: media("photo-04.jpg"),
        person_id: "sam",
        ignored: false,
      },
      {
        source_id: "face-taylor",
        source_name: "Taylor",
        thumbnail_url: media("photo-02.jpg"),
        person_id: "taylor",
        ignored: false,
      },
      {
        source_id: "face-grandpa",
        source_name: "Grandpa Joe",
        thumbnail_url: media("photo-06.jpg"),
        person_id: "",
        ignored: false,
      },
      {
        source_id: "face-unnamed",
        source_name: "",
        thumbnail_url: media("photo-09.jpg"),
        person_id: "",
        ignored: false,
      },
      {
        source_id: "face-neighbor",
        source_name: "Neighbor",
        thumbnail_url: media("photo-11.jpg"),
        person_id: "",
        ignored: true,
      },
    ],
  };
}
