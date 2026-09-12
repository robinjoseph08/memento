// PROTOTYPE variant C, "Sheet". A centered, gallery-wall presentation: the
// uncropped cover is the hero with the title beneath it, tabs are pills, and
// the media is a uniform square grid that fits the most on screen. Squares
// crop thumbnails; the lightbox shows the full image.
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
  Lightbox,
  PlayBadge,
  type MemberAlbum,
  type ViewerAlbum,
} from "./viewer-shared";
import { useVariantLink } from "./viewer-variants";

const container = "mx-auto max-w-[1440px] px-5 min-[761px]:px-12";

export function AlbumListC({ albums }: { albums: MemberAlbum[] }) {
  const link = useVariantLink();
  return (
    <div className={cn(container, "pt-10 pb-14 min-[761px]:pt-17")}>
      <h1 className={cn(headingClass, "text-center")}>Your albums</h1>
      {albums.length === 0 ? (
        <p className="mx-auto mt-9 max-w-120 text-center text-muted">
          There are no albums to view yet. Your Curator will choose what to
          share with you.
        </p>
      ) : (
        <ul className="mx-auto mt-10 grid max-w-[1100px] grid-cols-1 gap-8 min-[761px]:grid-cols-2">
          {albums.map((album) => (
            <li className="min-w-0" key={album.id}>
              <Link
                className="block cursor-pointer rounded-md p-2 text-center hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                to={link(`/albums/${album.id}`)}
              >
                <AlbumImage
                  alt={album.title}
                  className="aspect-[3/2] h-auto w-full object-cover"
                  fallback="No cover"
                  src={album.cover_url}
                />
                <span className="mt-4 block font-heading text-[27px]/[1.2] tracking-[-0.35px] wrap-anywhere">
                  {album.title}
                </span>
                <span className="mt-1 block text-sm text-muted">
                  {dateRange(album.start_date, album.end_date)}
                </span>
                <span className="mt-1 block text-xs text-muted">
                  {countLabel(album.photo_count, "photo", "photos")},{" "}
                  {countLabel(album.video_count, "video", "videos")}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function AlbumC({
  album,
  tab,
  mediaId,
}: {
  album: ViewerAlbum;
  tab: string;
  mediaId: string;
}) {
  const link = useVariantLink();
  const kind = tab === "videos" ? "VIDEO" : "IMAGE";
  const days = entriesOfKind(album, kind);
  const items = days.flatMap((day) => day.entries);
  const base = `/albums/${album.id}`;
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
        to={link("/albums")}
      >
        <ChevronLeft aria-hidden="true" className="size-4" strokeWidth={1.5} />
        All albums
      </Link>
      <header className="mt-6 flex flex-col items-center text-center min-[761px]:mt-8">
        <AlbumImage
          alt="Album cover"
          className="max-h-[56vh] w-auto max-w-full"
          fallback="No cover"
          src={album.cover_url}
        />
        <h1 className="mt-8 font-heading text-[clamp(36px,5vw,60px)] leading-[1.1] tracking-[-1.5px] text-balance">
          {album.title}
        </h1>
        {album.description && (
          <p className="mt-4 max-w-[560px] text-sm text-muted">
            {album.description}
          </p>
        )}
        <p className="mt-4 text-xs text-muted">
          {dateRange(album.start_date, album.end_date)}
        </p>
      </header>
      <nav
        aria-label="Album media"
        className="mt-10 flex justify-center gap-1 min-[761px]:mt-12"
      >
        {tabs.map((item) => {
          const active = kind === (item.key === "videos" ? "VIDEO" : "IMAGE");
          return (
            <Link
              aria-current={active ? "page" : undefined}
              className={cn(
                "inline-flex min-h-10 items-center gap-2 rounded-full border px-4 text-sm",
                active
                  ? "border-primary text-foreground"
                  : "border-border text-muted hover:bg-surface hover:text-foreground",
              )}
              key={item.key}
              to={link(`${base}/${item.key}`)}
            >
              <item.icon
                aria-hidden="true"
                className="size-4"
                strokeWidth={1.5}
              />
              {item.label}
              <span className="text-xs opacity-70">{item.count}</span>
            </Link>
          );
        })}
      </nav>
      {days.length === 0 ? (
        <section className="py-14 text-center">
          <h2 className="font-heading text-[27px]/[1.2]">
            No {kind === "VIDEO" ? "videos" : "photos"} in this album
          </h2>
          <p className="mx-auto mt-3 max-w-100 text-sm text-muted">
            {kind === "VIDEO"
              ? "You can see this album's photos in the Photos tab."
              : "You can watch this album's videos in the Videos tab."}
          </p>
          <Link
            className="mt-6 inline-flex min-h-11 items-center rounded-md border border-border px-4 text-sm hover:bg-surface"
            to={link(`${base}/${kind === "VIDEO" ? "photos" : "videos"}`)}
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
            <div className="flex items-baseline gap-3 border-b border-border pb-2">
              <h2
                className="font-heading text-xl tracking-[-0.2px]"
                id={`day-${day.date}`}
              >
                {dayLabel(day.date)}
              </h2>
              <span className="text-xs text-muted">
                {countLabel(
                  day.entries.length,
                  kind === "VIDEO" ? "video" : "photo",
                  kind === "VIDEO" ? "videos" : "photos",
                )}
              </span>
            </div>
            <ul className="mt-3 grid grid-cols-3 gap-1 min-[601px]:grid-cols-4 min-[1001px]:grid-cols-6">
              {day.entries.map((entry) => (
                <li className="min-w-0" key={entry.id}>
                  <Link
                    aria-label={`Open ${kind === "VIDEO" ? "video" : "photo"} ${entry.title}`}
                    className="group relative block aspect-square overflow-hidden rounded-[2px] bg-surface focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    state={{ fromGrid: true }}
                    to={link(
                      `${base}/${kind === "VIDEO" ? "videos" : "photos"}/${entry.id}`,
                    )}
                  >
                    <img
                      alt={entry.filename}
                      className="size-full object-cover"
                      draggable={false}
                      loading="lazy"
                      src={entry.thumbnail_url}
                    />
                    {kind === "VIDEO" && (
                      <>
                        <PlayBadge className="inset-0 m-auto size-12 transition-colors group-hover:bg-primary group-hover:text-primary-foreground" />
                        <span className="absolute inset-x-0 bottom-0 truncate bg-black/60 px-2 py-1.5 text-left text-xs text-white">
                          {entry.title}
                          {entry.chapters.length > 0 &&
                            `, ${countLabel(entry.chapters.length, "chapter", "chapters")}`}
                        </span>
                      </>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}
      {mediaId && (
        <Lightbox
          closeTo={link(`${base}/${tab || "photos"}`)}
          current={mediaId}
          items={items}
          linkTo={(entry) => link(`${base}/${tab || "photos"}/${entry.id}`)}
          title={album.title}
        />
      )}
    </div>
  );
}
