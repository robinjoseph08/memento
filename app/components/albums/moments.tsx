import { Video } from "lucide-react";
import { useSearchParams } from "react-router-dom";

import { cn } from "../../lib/utils";
import type { Entry, Moment } from "../../types/generated/publishing";
import { sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";

export function MediaCounts({ entries }: { entries: Entry[] }) {
  const photos = entries.filter((entry) => entry.kind === "IMAGE").length;
  const videos = entries.filter((entry) => entry.kind === "VIDEO").length;
  return `${photos} ${photos === 1 ? "photo" : "photos"}, ${videos} ${videos === 1 ? "video" : "videos"}`;
}

function momentHeading(moment: Moment) {
  const date = new Date(`${moment.date}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) return { title: moment.label, date: "" };
  const options = {
    timeZone: "UTC",
    month: "long",
    day: "numeric",
    year: "numeric",
  } as const;
  const original = date.toLocaleDateString("en-US", options);
  const dated = date.toLocaleDateString("en-US", {
    ...options,
    weekday: "long",
  });
  return {
    title: moment.label === original ? dated : moment.label,
    date: moment.label === original || moment.label === dated ? "" : dated,
  };
}

export function Moments({ moments }: { moments: Moment[] }) {
  const [params, setParams] = useSearchParams();
  const selected = params.get("moment") ?? moments[0]?.id;
  function toggle(id: string) {
    const next = new URLSearchParams(params);
    next.set("moment", selected === id ? "none" : id);
    next.delete("media");
    setParams(next);
  }
  return (
    <section aria-labelledby="moments-heading">
      <div className="flex items-center justify-between gap-4">
        <h2 className={sectionHeadingClass} id="moments-heading">
          Moments
        </h2>
        <p className="text-xs text-muted">
          {moments.length} {moments.length === 1 ? "Moment" : "Moments"}
        </p>
      </div>
      <p className="mt-2 mb-6 max-w-180 text-xs leading-relaxed text-muted">
        Moments are private groups for curation. This import created one per
        local capture day. Times shown below are local capture times.
      </p>
      {moments.length === 0 ? (
        <p className="py-6 text-sm text-muted">
          This Album had no photos or videos to import.
        </p>
      ) : (
        <div className="space-y-3">
          {moments.map((moment) => {
            const expanded = moment.id === selected;
            const heading = momentHeading(moment);
            const cover = moment.entries.find(
              (entry) => entry.id === moment.cover_entry_id,
            );
            const all = params.get("media") === "all";
            const visible = all ? moment.entries : moment.entries.slice(0, 24);
            return (
              <section
                aria-labelledby={`moment-${moment.id}`}
                className={cn(
                  "overflow-hidden rounded-md border",
                  expanded ? "border-primary" : "border-border",
                )}
                key={moment.id}
              >
                <h3>
                  <button
                    aria-controls={`moment-media-${moment.id}`}
                    aria-expanded={expanded}
                    aria-labelledby={`moment-${moment.id}`}
                    className="flex w-full cursor-pointer items-center gap-3 bg-surface px-3 py-3 text-left hover:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                    onClick={() => toggle(moment.id)}
                    type="button"
                  >
                    <AlbumImage
                      alt=""
                      className="h-auto max-h-11 w-auto max-w-20 shrink-0"
                      fallback="No cover"
                      src={cover?.available ? cover.thumbnail_url : ""}
                    />
                    <span className="min-w-0 flex-1">
                      <span
                        className="block font-heading text-xl leading-tight min-[761px]:text-2xl"
                        id={`moment-${moment.id}`}
                      >
                        {heading.title}
                      </span>
                      {heading.date && (
                        <span className="mt-1 block text-xs font-normal text-muted">
                          {heading.date}
                        </span>
                      )}
                      <span className="mt-1 block text-xs font-normal text-muted">
                        <MediaCounts entries={moment.entries} />
                      </span>
                    </span>
                    <svg
                      aria-hidden="true"
                      className={cn("size-4 shrink-0", expanded && "rotate-90")}
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      viewBox="0 0 24 24"
                    >
                      <path d="m9 6 6 6-6 6" />
                    </svg>
                  </button>
                </h3>
                {expanded && (
                  <div className="p-3" id={`moment-media-${moment.id}`}>
                    <ul
                      aria-label="Moment media"
                      className="flex flex-wrap items-start gap-x-2 gap-y-4"
                    >
                      {visible.map((entry) => (
                        <li
                          className="max-w-full min-w-0 flex-none"
                          key={entry.id}
                        >
                          <figure className="relative min-w-8 pb-5">
                            <div className="relative w-fit max-w-full">
                              <AlbumImage
                                alt={entry.filename}
                                className="h-auto max-h-24 w-auto max-w-full rounded-sm min-[1101px]:max-h-28"
                                fallback={
                                  entry.available
                                    ? "No preview available"
                                    : "Unavailable in Immich"
                                }
                                src={entry.available ? entry.thumbnail_url : ""}
                              />
                              {entry.id === moment.cover_entry_id && (
                                <span
                                  className="absolute top-1 left-1 max-w-[calc(100%-8px)] truncate rounded-sm bg-black/70 px-1.5 py-0.5 text-[10px] text-white"
                                  title="Moment cover"
                                >
                                  Cover
                                </span>
                              )}
                              {entry.kind === "VIDEO" && (
                                <span
                                  aria-label="Video"
                                  className="absolute right-1 bottom-1 rounded-sm bg-black/70 p-1 text-white"
                                  role="img"
                                >
                                  <Video
                                    aria-hidden="true"
                                    className="size-3.5"
                                  />
                                </span>
                              )}
                            </div>
                            <figcaption className="absolute inset-x-0 bottom-0 truncate text-[10px] text-muted">
                              <time
                                dateTime={entry.captured_at}
                                title={entry.captured_at.replace("T", " ")}
                              >
                                {entry.captured_at.slice(11, 16)}
                              </time>
                            </figcaption>
                          </figure>
                        </li>
                      ))}
                    </ul>
                    {moment.entries.length > 24 && (
                      <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-border pt-3">
                        <p className="text-xs text-muted">
                          {visible.length} of {moment.entries.length} items
                          shown
                        </p>
                        <Button
                          className="text-xs"
                          onClick={() => {
                            const next = new URLSearchParams(params);
                            if (all) next.delete("media");
                            else next.set("media", "all");
                            setParams(next);
                          }}
                          variant="ghost"
                        >
                          {all
                            ? "Show fewer items"
                            : `Show all ${moment.entries.length} items`}
                        </Button>
                      </div>
                    )}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      )}
    </section>
  );
}
