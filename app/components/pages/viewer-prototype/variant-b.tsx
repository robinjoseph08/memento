// PROTOTYPE variant B, "Timeline". The album is one chronological stream.
// Photos and videos sit together in justified rows, each day has a date rail
// at the left that stays put while its rows scroll, and a filter row replaces
// tabs. There is no separate cover: the first day's media is the hero.
import { ChevronLeft, Image, SquarePlay } from "lucide-react";
import { Link } from "react-router-dom";

import { cn } from "../../../lib/utils";
import { AlbumImage } from "../../albums/album-image";
import { headingClass } from "../../people/form-fields";
import {
  allEntries,
  countLabel,
  dateRange,
  JustifiedRow,
  justifiedRows,
  Lightbox,
  PlayBadge,
  ratio,
  useTargetRatio,
  type MemberAlbum,
  type ViewerAlbum,
} from "./viewer-shared";
import { useVariantLink } from "./viewer-variants";

const container = "mx-auto max-w-[1440px] px-5 min-[761px]:px-12";

function shortDay(day: string) {
  const date = new Date(`${day}T00:00:00Z`);
  return {
    weekday: date.toLocaleDateString("en-US", {
      timeZone: "UTC",
      weekday: "short",
    }),
    day: date.toLocaleDateString("en-US", { timeZone: "UTC", day: "numeric" }),
    month: date.toLocaleDateString("en-US", {
      timeZone: "UTC",
      month: "short",
    }),
    year: date.toLocaleDateString("en-US", {
      timeZone: "UTC",
      year: "numeric",
    }),
  };
}

export function AlbumListB({ albums }: { albums: MemberAlbum[] }) {
  const link = useVariantLink();
  return (
    <div className={cn(container, "pt-10 pb-14 min-[761px]:pt-17")}>
      <h1 className={headingClass}>Your albums</h1>
      {albums.length === 0 ? (
        <p className="mt-9 max-w-120 text-muted">
          There are no albums to view yet. Your Curator will choose what to
          share with you.
        </p>
      ) : (
        <ul className="mt-8 border-t border-border">
          {albums.map((album) => (
            <li className="border-b border-border" key={album.id}>
              <Link
                className="-mx-3 flex cursor-pointer items-center gap-5 rounded-md px-3 py-4 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring min-[601px]:gap-8"
                to={link(`/albums/${album.id}`)}
              >
                <AlbumImage
                  alt={album.title}
                  className="w-28 shrink-0 object-cover min-[601px]:w-44"
                  fallback="No cover"
                  src={album.cover_url}
                />
                <span className="min-w-0 flex-1">
                  <span className="block font-heading text-[27px]/[1.2] tracking-[-0.35px] wrap-anywhere">
                    {album.title}
                  </span>
                  <span className="mt-1 block text-sm text-muted">
                    {dateRange(album.start_date, album.end_date)}
                  </span>
                  <span className="mt-2 block text-xs text-muted">
                    {countLabel(album.photo_count, "photo", "photos")},{" "}
                    {countLabel(album.video_count, "video", "videos")}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function AlbumB({
  album,
  tab,
  mediaId,
}: {
  album: ViewerAlbum;
  tab: string;
  mediaId: string;
}) {
  const link = useVariantLink();
  const filter =
    tab === "photos" ? "IMAGE" : tab === "videos" ? "VIDEO" : "ALL";
  const base = `/albums/${album.id}`;
  const path = tab || "all";
  const days = album.days
    .map((day) => ({
      ...day,
      entries: day.entries.filter(
        (entry) => filter === "ALL" || entry.kind === filter,
      ),
    }))
    .filter((day) => day.entries.length > 0);
  const items = days.flatMap((day) => day.entries);
  const target = useTargetRatio(4.5, 3, 1.5);
  const filters = [
    { key: "all", label: "All", count: allEntries(album).length },
    { key: "photos", label: "Photos", count: album.photo_count },
    { key: "videos", label: "Videos", count: album.video_count },
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
      <header className="mt-6 max-w-[760px] min-[761px]:mt-8">
        <h1 className="font-heading text-[clamp(32px,4vw,44px)] leading-[1.15] tracking-[-1px] text-balance">
          {album.title}
        </h1>
        {album.description && (
          <p className="mt-3 text-sm text-muted">{album.description}</p>
        )}
        <p className="mt-3 text-xs text-muted">
          {dateRange(album.start_date, album.end_date)}
        </p>
      </header>
      <div className="sticky top-0 z-10 -mx-5 mt-6 border-b border-border bg-background px-5 py-2 min-[761px]:-mx-12 min-[761px]:px-12">
        <nav aria-label="Show" className="flex gap-1">
          {filters.map((item) => {
            const active = path === item.key;
            return (
              <Link
                aria-current={active ? "page" : undefined}
                className={cn(
                  "inline-flex min-h-9 items-center gap-2 rounded-full px-3.5 text-sm",
                  active
                    ? "bg-accent text-foreground"
                    : "text-muted hover:bg-surface hover:text-foreground",
                )}
                key={item.key}
                to={link(`${base}/${item.key}`)}
              >
                {item.label}
                <span className="text-xs opacity-70">{item.count}</span>
              </Link>
            );
          })}
        </nav>
      </div>
      {days.length === 0 ? (
        <p className="py-14 text-sm text-muted">
          Nothing here yet. Try another filter.
        </p>
      ) : (
        days.map((day) => {
          const label = shortDay(day.date);
          return (
            <section
              aria-label={`${label.weekday}, ${label.month} ${label.day}, ${label.year}`}
              className="grid grid-cols-1 gap-x-8 pt-8 min-[761px]:grid-cols-[104px_minmax(0,1fr)]"
              key={day.date}
            >
              <div className="sticky top-[53px] z-[5] self-start bg-background pb-3 min-[761px]:top-16 min-[761px]:bg-transparent min-[761px]:pb-0">
                <p className="flex items-baseline gap-2 min-[761px]:block">
                  <span className="font-heading text-[34px] leading-none tracking-[-0.5px]">
                    {label.day}
                  </span>
                  <span className="text-sm min-[761px]:mt-1 min-[761px]:block">
                    {label.weekday}, {label.month}
                  </span>
                  <span className="text-xs text-muted min-[761px]:block">
                    {label.year}
                  </span>
                </p>
                <p className="mt-1 text-xs text-muted min-[761px]:mt-3">
                  {countLabel(
                    day.entries.filter((entry) => entry.kind === "IMAGE")
                      .length,
                    "photo",
                    "photos",
                  )}
                  {day.entries.some((entry) => entry.kind === "VIDEO") && (
                    <>
                      <br className="hidden min-[761px]:block" />
                      <span className="min-[761px]:hidden">, </span>
                      {countLabel(
                        day.entries.filter((entry) => entry.kind === "VIDEO")
                          .length,
                        "video",
                        "videos",
                      )}
                    </>
                  )}
                </p>
              </div>
              <div className="flex flex-col gap-1">
                {justifiedRows(day.entries, target).map((row, index, rows) => (
                  <JustifiedRow
                    key={row[0].id}
                    last={index === rows.length - 1}
                    row={row}
                    targetRatio={target}
                  >
                    {(entry) => (
                      <Link
                        aria-label={`Open ${entry.kind === "VIDEO" ? "video" : "photo"} ${entry.title}`}
                        className="relative block focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        state={{ fromGrid: true }}
                        to={link(`${base}/${path}/${entry.id}`)}
                      >
                        <img
                          alt={entry.filename}
                          className="block w-full rounded-[2px] bg-surface"
                          draggable={false}
                          loading="lazy"
                          src={entry.thumbnail_url}
                          style={{ aspectRatio: ratio(entry) }}
                        />
                        {entry.kind === "VIDEO" && (
                          <>
                            <PlayBadge className="inset-0 m-auto size-12" />
                            <span className="absolute right-2 bottom-2 flex items-center gap-1 rounded-sm bg-black/60 px-1.5 py-0.5 text-[11px] text-white">
                              <SquarePlay
                                aria-hidden="true"
                                className="size-3.5"
                                strokeWidth={1.5}
                              />
                              {entry.title}
                            </span>
                          </>
                        )}
                        {entry.kind === "IMAGE" && filter === "ALL" && (
                          <span className="sr-only">
                            <Image aria-hidden="true" />
                          </span>
                        )}
                      </Link>
                    )}
                  </JustifiedRow>
                ))}
              </div>
            </section>
          );
        })
      )}
      {mediaId && (
        <Lightbox
          closeTo={link(`${base}/${path}`)}
          current={mediaId}
          items={items}
          linkTo={(entry) => link(`${base}/${path}/${entry.id}`)}
          title={album.title}
        />
      )}
    </div>
  );
}
