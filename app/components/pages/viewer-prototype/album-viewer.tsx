// PROTOTYPE. The chosen viewer: the approved design rebuilt on the real shell.
// An explicit All albums action, a large left-aligned heading with the plain
// uncropped cover at the right, Photos and Videos tabs with counts, and
// justified rows with a 4px gap under day headings.
import { ChevronLeft, Image, SquarePlay } from "lucide-react";
import { Link } from "react-router-dom";

import { cn } from "../../../lib/utils";
import { AlbumImage } from "../../albums/album-image";
import { headingClass } from "../../people/form-fields";
import {
  countLabel,
  dateRange,
  dayLabel,
  entriesOfKind,
  JustifiedRow,
  justifiedRows,
  Lightbox,
  PlayBadge,
  ratio,
  useTargetRatio,
  type MemberAlbum,
  type ViewerAlbum,
} from "./viewer-shared";

const container = "mx-auto max-w-[1440px] px-5 min-[761px]:px-12";

export function AlbumList({ albums }: { albums: MemberAlbum[] }) {
  return (
    <div className={cn(container, "pt-10 pb-14 min-[761px]:pt-17")}>
      <h1 className={headingClass}>Your albums</h1>
      {albums.length === 0 ? (
        <section className="mt-9 border-t border-border py-9">
          <h2 className="font-heading text-[27px]/[1.2]">No albums yet</h2>
          <p className="mt-4 max-w-120 text-muted">
            There are no albums to view yet. Your Curator will choose what to
            share with you.
          </p>
        </section>
      ) : (
        <ul className="mt-9 grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4">
          {albums.map((album) => (
            <li className="min-w-0" key={album.id}>
              <Link
                className="block cursor-pointer rounded-sm p-2 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                to={`/albums/${album.id}`}
              >
                <AlbumImage
                  alt={album.title}
                  className="aspect-square h-auto w-full object-cover"
                  fallback="No cover"
                  src={album.cover_url}
                />
                <span className="mt-3 block font-heading text-lg wrap-anywhere">
                  {album.title}
                </span>
                <span className="block text-xs text-muted">
                  {countLabel(album.photo_count, "photo", "photos")},{" "}
                  {countLabel(album.video_count, "video", "videos")}
                </span>
                <span className="block text-xs text-muted">
                  {dateRange(album.start_date, album.end_date)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function AlbumView({
  album,
  tab,
  mediaId,
}: {
  album: ViewerAlbum;
  tab: string;
  mediaId: string;
}) {
  const kind = tab === "videos" ? "VIDEO" : "IMAGE";
  const days = entriesOfKind(album, kind);
  const items = days.flatMap((day) => day.entries);
  const base = `/albums/${album.id}`;
  const target = useTargetRatio();
  const tabs = [
    { key: "photos", label: "Photos", count: album.photo_count, icon: Image },
    {
      key: "videos",
      label: "Videos",
      count: album.video_count,
      icon: SquarePlay,
    },
  ];
  return (
    <div className={cn(container, "pt-6 pb-16")}>
      <Link
        className="-ml-2 inline-flex min-h-9 items-center gap-1 rounded-md px-2 text-sm text-muted hover:bg-surface hover:text-foreground"
        to={"/albums"}
      >
        <ChevronLeft aria-hidden="true" className="size-4" strokeWidth={1.5} />
        All albums
      </Link>
      <header className="mt-6 min-[761px]:mt-10 min-[761px]:grid min-[761px]:grid-cols-[minmax(0,1fr)_minmax(0,390px)] min-[761px]:items-center min-[761px]:gap-12">
        <div className="min-w-0">
          <div className="flex items-start gap-5">
            <h1 className="min-w-0 flex-1 font-heading text-[clamp(36px,5vw,64px)] leading-[1.1] tracking-[-1.5px] text-balance">
              {album.title}
            </h1>
            <AlbumImage
              alt="Album cover"
              className="w-28 shrink-0 min-[761px]:hidden"
              fallback="No cover"
              src={album.cover_url}
            />
          </div>
          {album.description && (
            <p className="mt-5 max-w-[560px] text-sm text-muted min-[761px]:mt-7">
              {album.description}
            </p>
          )}
          <p className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted">
            <span>{dateRange(album.start_date, album.end_date)}</span>
            <span className="inline-flex items-center gap-1.5">
              <Image
                aria-hidden="true"
                className="size-3.5"
                strokeWidth={1.5}
              />
              {countLabel(album.photo_count, "photo", "photos")}
            </span>
            <span className="inline-flex items-center gap-1.5">
              <SquarePlay
                aria-hidden="true"
                className="size-3.5"
                strokeWidth={1.5}
              />
              {countLabel(album.video_count, "video", "videos")}
            </span>
          </p>
        </div>
        <AlbumImage
          alt="Album cover"
          className="hidden w-full min-[761px]:block"
          fallback="No cover"
          src={album.cover_url}
        />
      </header>
      <nav
        aria-label="Album media"
        className="mt-10 flex border-b border-border min-[761px]:mt-14"
      >
        {tabs.map((item) => (
          <Link
            aria-current={
              kind === (item.key === "videos" ? "VIDEO" : "IMAGE")
                ? "page"
                : undefined
            }
            className={cn(
              "-mb-px inline-flex items-center gap-2 border-b-2 px-[18px] py-3 text-sm",
              kind === (item.key === "videos" ? "VIDEO" : "IMAGE")
                ? "border-primary text-foreground"
                : "border-transparent text-muted hover:text-foreground",
            )}
            key={item.key}
            to={`${base}/${item.key}`}
          >
            <item.icon
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            {item.label}
            <span
              className={cn(
                "rounded-sm px-1.5 text-xs",
                kind === (item.key === "videos" ? "VIDEO" : "IMAGE")
                  ? "bg-primary/15 text-accent-foreground"
                  : "bg-surface",
              )}
            >
              {item.count}
            </span>
          </Link>
        ))}
      </nav>
      {days.length === 0 ? (
        <section className="py-14">
          <h2 className="font-heading text-[27px]/[1.2]">
            No {kind === "VIDEO" ? "videos" : "photos"} in this album
          </h2>
          <p className="mt-3 max-w-100 text-sm text-muted">
            {kind === "VIDEO"
              ? "You can see this album's photos in the Photos tab."
              : "You can watch this album's videos in the Videos tab."}
          </p>
          <Link
            className="mt-6 inline-flex min-h-11 items-center rounded-md border border-border px-4 text-sm hover:bg-surface"
            to={`${base}/${kind === "VIDEO" ? "photos" : "videos"}`}
          >
            View {kind === "VIDEO" ? "photos" : "videos"}
          </Link>
        </section>
      ) : (
        days.map((day) => (
          <section
            aria-labelledby={`day-${day.date}`}
            className="pt-10"
            key={day.date}
          >
            <h2
              className="font-heading text-[27px]/[1.2] tracking-[-0.35px]"
              id={`day-${day.date}`}
            >
              {dayLabel(day.date)}
              <span className="ml-3 font-sans text-xs tracking-normal whitespace-nowrap text-muted">
                {countLabel(
                  day.entries.length,
                  kind === "VIDEO" ? "video" : "photo",
                  kind === "VIDEO" ? "videos" : "photos",
                )}
              </span>
            </h2>
            {kind === "IMAGE" ? (
              <div className="mt-5 flex flex-col gap-1">
                {justifiedRows(day.entries, target).map((row, index, rows) => (
                  <JustifiedRow
                    key={row[0].id}
                    last={index === rows.length - 1}
                    row={row}
                    targetRatio={target}
                  >
                    {(entry) => (
                      <Link
                        aria-label={`Open photo ${entry.filename}`}
                        className="block focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        state={{ fromGrid: true }}
                        to={`${base}/photos/${entry.id}`}
                      >
                        <img
                          alt={entry.filename}
                          className="block w-full rounded-[2px] bg-surface"
                          draggable={false}
                          loading="lazy"
                          src={entry.thumbnail_url}
                          style={{ aspectRatio: ratio(entry) }}
                        />
                      </Link>
                    )}
                  </JustifiedRow>
                ))}
              </div>
            ) : (
              <ul className="mt-5 grid grid-cols-1 gap-x-4 gap-y-6 min-[601px]:grid-cols-2 min-[1001px]:grid-cols-3">
                {day.entries.map((entry) => (
                  <li key={entry.id}>
                    <Link
                      aria-label={`Open video ${entry.title}`}
                      className="group block rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                      state={{ fromGrid: true }}
                      to={`${base}/videos/${entry.id}`}
                    >
                      <span className="relative block overflow-hidden rounded-[2px] bg-surface">
                        <img
                          alt=""
                          className="block w-full"
                          draggable={false}
                          src={entry.thumbnail_url}
                          style={{ aspectRatio: ratio(entry) }}
                        />
                        <PlayBadge className="inset-0 m-auto size-12 transition-colors group-hover:bg-primary group-hover:text-primary-foreground" />
                      </span>
                      <span className="mt-3 block font-heading text-lg leading-tight">
                        {entry.title}
                      </span>
                      {entry.chapters.length > 0 && (
                        <span className="block text-xs text-muted">
                          {countLabel(
                            entry.chapters.length,
                            "chapter",
                            "chapters",
                          )}
                        </span>
                      )}
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ))
      )}
      {mediaId && (
        <Lightbox
          closeTo={`${base}/${tab || "photos"}`}
          current={mediaId}
          items={items}
          linkTo={(entry) => `${base}/${tab || "photos"}/${entry.id}`}
          title={album.title}
        />
      )}
    </div>
  );
}
