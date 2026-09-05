// Fictional viewer fixtures. All bundled images and clips are generated illustrations.
const captures = [
  ["2025-06-14", "The lake in the morning", 1.5],
  ["2025-06-14", "Hills above the water", 1.5],
  ["2025-06-14", "A pine beside the shore", 1.5],
  ["2025-06-14", "Sunlight across the lake", 1.5],
  ["2025-06-14", "The far shore", 1.5],
  ["2025-06-14", "Looking across the valley", 1.5],
  ["2025-06-14", "The view from the cabin", 1.5],
  ["2025-06-14", "A quiet stretch of water", 1.5],
  ["2025-06-14", "Pines on the headland", 1.5],
  ["2025-06-14", "The afternoon light", 1.5],
  ["2025-06-14", "A tall pine above the water", 2 / 3],
  ["2025-06-14", "The lake before sunset", 1.5],
  ["2025-06-15", "Morning by the shore", 1.5],
  ["2025-06-15", "The hills from the picnic spot", 1.5],
  ["2025-06-15", "A sunny afternoon", 1.5],
  ["2025-06-15", "The view along the trail", 1.5],
  ["2025-06-15", "Water under the hills", 1.5],
  ["2025-06-15", "A pine reflected in the lake", 2 / 3],
  ["2025-06-15", "The headland from the trail", 1.5],
  ["2025-06-15", "Late afternoon at the cabin", 1.5],
  ["2025-06-15", "An evening walk", 1.5],
  ["2025-06-15", "The last light on the water", 2 / 3],
  ["2025-06-16", "The lake before leaving", 1.5],
  ["2025-06-16", "One last look at the pines", 2 / 3],
  ["2025-06-16", "Hills on the drive home", 1.5],
] as const;

export const photos = captures.map(([day, alt, ratio], index) => ({
  id: `p${String(index + 1).padStart(2, "0")}`,
  image: `/viewer-prototype/photo-${String(index + 1).padStart(2, "0")}.jpg`,
  day,
  alt,
  ratio,
}));

export const videos = [
  {
    id: "v01",
    title: "An afternoon by the lake",
    filename: "CLIP_001.mp4",
    day: "2025-06-14",
    poster: "/viewer-prototype/video-01.jpg",
    src: "/viewer-prototype/clip-01.mp4",
    chapters: [
      { title: "The shore", time: 0 },
      { title: "Across the water", time: 10 },
      { title: "The far hills", time: 20 },
    ],
  },
  {
    id: "v02",
    title: "",
    filename: "CLIP_002.mp4",
    day: "2025-06-14",
    poster: "/viewer-prototype/video-02.jpg",
    src: "/viewer-prototype/clip-02.mp4",
    chapters: [],
  },
];

export const album = {
  title: "A weekend by the lake",
  description:
    "Two nights at the cabin. Lakeside walks, a picnic in the shade, and a slow drive home.",
  dateRange: "June 14 to June 16, 2025",
  videoDateRange: "June 14, 2025",
  cover: photos[6],
};
export const videoTitle = (video: (typeof videos)[number]) =>
  video.title || video.filename.replace(/\.[^.]+$/, "");
export const dayLabel = (day: string) =>
  new Date(`${day}T12:00:00`).toLocaleDateString("en-US", {
    weekday: "long",
    month: "long",
    day: "numeric",
    year: "numeric",
  });
