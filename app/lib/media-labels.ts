type MediaLabelSource = {
  kind: string;
  captured_at: string;
  filename?: string;
  title?: string;
};

const captureDayFormatter = new Intl.DateTimeFormat("en-US", {
  timeZone: "UTC",
  month: "long",
  day: "numeric",
  year: "numeric",
});

export function captureClock(capturedAt: string) {
  const hour = Number(capturedAt.slice(11, 13));
  const clock = `${hour % 12 || 12}:${capturedAt.slice(14, 16)}`;
  return `${clock} ${hour < 12 ? "AM" : "PM"}`;
}

function captureDay(capturedAt: string) {
  const date = new Date(`${capturedAt.slice(0, 10)}T12:00:00Z`);
  if (Number.isNaN(date.getTime())) return "";
  return captureDayFormatter.format(date);
}

export function filenameTitle(filename: string) {
  return filename.replace(/\.[^.]+$/, "");
}

export function photoLabel(capturedAt: string, noun = "Photo") {
  const day = captureDay(capturedAt);
  return day ? `${noun} taken ${day} at ${captureClock(capturedAt)}` : noun;
}

export function mediaLabel(media: MediaLabelSource) {
  if (media.kind === "VIDEO")
    return (
      media.title?.trim() || filenameTitle(media.filename ?? "") || "Video"
    );
  return photoLabel(media.captured_at);
}

export function disambiguatePhotoLabels(labels: string[]) {
  const totals = new Map<string, number>();
  for (const label of labels) totals.set(label, (totals.get(label) ?? 0) + 1);
  const seen = new Map<string, number>();
  return labels.map((label) => {
    if (!label.startsWith("Photo taken ") || totals.get(label) === 1)
      return label;
    const position = (seen.get(label) ?? 0) + 1;
    seen.set(label, position);
    return `Photo ${position}${label.slice("Photo".length)}`;
  });
}
