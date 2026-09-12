import { Image, Video } from "lucide-react";
import { Link, type To } from "react-router-dom";

import {
  useViewerAlbum,
  useViewerEntries,
  type ViewerContext,
  type ViewerTab,
} from "../../hooks/queries/viewer";
import { useMediaQuery } from "../../hooks/use-media-query";
import { HTTPError } from "../../lib/http";
import { cn } from "../../lib/utils";
import type {
  ViewerAlbum,
  ViewerEntry,
} from "../../types/generated/publishing";
import { AlbumImage } from "../albums/album-image";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumHeader } from "./album-header";
import { captureDate, countLabel } from "./labels";

export function ViewerGallery({
  context,
  tab,
  tabLinks,
}: {
  context: ViewerContext;
  tab: ViewerTab;
  tabLinks: Record<ViewerTab, To>;
}) {
  const query = useViewerAlbum(context);
  const noAccess =
    query.error instanceof HTTPError && query.error.status === 404;
  return (
    <>
      <PageTitle
        title={
          query.data?.title ?? (context.personID ? "Viewer preview" : "Album")
        }
      />
      {query.isPending ? (
        <p role="status">Loading album…</p>
      ) : query.isError ? (
        <section className="py-10">
          <h1 className="font-heading text-3xl">
            {noAccess ? "Album not available" : "Could not load album"}
          </h1>
          <p className="mt-3 text-muted">
            {noAccess
              ? context.personID
                ? "This person has no access to this album."
                : "This album is not available to you."
              : "Please try again."}
          </p>
          {!noAccess && (
            <Button
              className="mt-4"
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
              variant="outline"
            >
              Try again
            </Button>
          )}
        </section>
      ) : (
        <>
          <AlbumHeader album={query.data} />
          <nav
            aria-label="Album media"
            className="mt-10 flex border-b border-border min-[761px]:mt-14"
          >
            {(["photos", "videos"] as const).map((value) => {
              const Icon = value === "photos" ? Image : Video;
              return (
                <Link
                  aria-current={tab === value ? "page" : undefined}
                  className={cn(
                    "-mb-px inline-flex items-center gap-2 border-b-2 px-[18px] py-3 text-sm hover:bg-surface",
                    tab === value
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted",
                  )}
                  key={value}
                  to={tabLinks[value]}
                >
                  <Icon
                    aria-hidden="true"
                    className="size-4"
                    strokeWidth={1.5}
                  />
                  {value === "photos" ? "Photos" : "Videos"}{" "}
                  <span
                    className={cn(
                      "rounded-sm px-1.5 text-xs",
                      tab === value
                        ? "bg-primary/15 text-accent-foreground"
                        : "bg-surface",
                    )}
                  >
                    {value === "photos"
                      ? query.data.photo_count
                      : query.data.video_count}
                  </span>
                </Link>
              );
            })}
          </nav>
          <GalleryEntries
            album={query.data}
            context={context}
            tab={tab}
            tabLinks={tabLinks}
          />
        </>
      )}
    </>
  );
}

function GalleryEntries({
  album,
  context,
  tab,
  tabLinks,
}: {
  album: ViewerAlbum;
  context: ViewerContext;
  tab: ViewerTab;
  tabLinks: Record<ViewerTab, To>;
}) {
  const query = useViewerEntries(context, tab);
  if (
    query.error instanceof HTTPError &&
    (query.error.status === 403 || query.error.status === 404)
  ) {
    return (
      <p className="py-10 text-muted" role="alert">
        This album is no longer available to you.
      </p>
    );
  }
  const entries = query.data?.pages.flatMap((page) => page.entries) ?? [];
  const other = tab === "photos" ? "videos" : "photos";
  const kind = tab === "photos" ? "photo" : "video";
  return (
    <>
      {query.isPending && (
        <p className="py-10 text-muted" role="status">
          Loading {tab}…
        </p>
      )}
      {query.isError && (
        <div className="py-8">
          <p role="alert">Could not load {tab}. Please try again.</p>
          <Button
            className="mt-3"
            disabled={query.isFetching}
            onClick={() =>
              void (query.isFetchNextPageError
                ? query.fetchNextPage()
                : query.refetch())
            }
            variant="outline"
          >
            Try again
          </Button>
        </div>
      )}
      {query.isSuccess && entries.length === 0 && (
        <section className="py-14">
          <h2 className="font-heading text-[27px]/[1.2]">
            No {tab} in this album
          </h2>
          <p className="mt-3 text-sm text-muted">
            You can see this album's {other} in the{" "}
            {other === "photos" ? "Photos" : "Videos"} tab.
          </p>
          <Link
            className="mt-5 inline-flex min-h-11 items-center rounded-md border border-border px-4 text-sm hover:bg-surface"
            to={tabLinks[other]}
          >
            View {other}
          </Link>
        </section>
      )}
      {album.days.map((day) => {
        const items = entries.filter(
          (entry) => entry.captured_at.slice(0, 10) === day.date.slice(0, 10),
        );
        if (!items.length) return null;
        return (
          <section
            aria-label={captureDate(day.date, true)}
            className="pt-10"
            key={day.date}
          >
            <h2 className="font-heading text-[27px]/[1.2] tracking-[-0.35px]">
              {captureDate(day.date, true)}{" "}
              <span className="ml-3 font-sans text-xs tracking-normal whitespace-nowrap text-muted">
                {countLabel(
                  tab === "photos" ? day.photo_count : day.video_count,
                  kind,
                )}
              </span>
            </h2>
            {tab === "photos" ? (
              <PhotoRows entries={items} />
            ) : (
              <ul
                aria-label="Videos"
                className="mt-5 grid grid-cols-1 gap-x-4 gap-y-6 min-[601px]:grid-cols-2 min-[1001px]:grid-cols-3"
              >
                {items.map((entry) => (
                  <li key={entry.id}>
                    <MediaThumbnail entry={entry} />
                    <h3 className="mt-3 font-heading text-lg">{entry.title}</h3>
                    <p className="mt-1 flex items-center gap-1.5 text-xs text-muted">
                      <Video aria-hidden="true" className="size-3.5" />
                      Video
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </section>
        );
      })}
      {query.hasNextPage && !query.isFetchNextPageError && (
        <Button
          className="mt-8"
          disabled={query.isFetchingNextPage}
          onClick={() => void query.fetchNextPage()}
          variant="outline"
        >
          {query.isFetchingNextPage ? `Loading ${tab}…` : `Load more ${tab}`}
        </Button>
      )}
    </>
  );
}

function aspectRatio(entry: ViewerEntry) {
  return entry.width > 0 && entry.height > 0 ? entry.width / entry.height : 1.5;
}

function MediaThumbnail({ entry }: { entry: ViewerEntry }) {
  return (
    <div style={{ aspectRatio: aspectRatio(entry) }}>
      <AlbumImage
        alt={entry.title || "Photo"}
        className="h-full w-full"
        fallback="Media unavailable"
        src={entry.available ? entry.thumbnail_url : ""}
      />
    </div>
  );
}

function PhotoRows({ entries }: { entries: ViewerEntry[] }) {
  const desktop = useMediaQuery("(min-width: 1001px)");
  const tablet = useMediaQuery("(min-width: 601px)");
  const target = desktop ? 4.5 : tablet ? 3 : 1.5;
  const rows: ViewerEntry[][] = [];
  let row: ViewerEntry[] = [];
  let total = 0;
  for (const entry of entries) {
    const ratio = aspectRatio(entry);
    if (
      row.length &&
      Math.abs(total - target) < Math.abs(total + ratio - target)
    ) {
      rows.push(row);
      row = [];
      total = 0;
    }
    row.push(entry);
    total += ratio;
  }
  if (row.length) rows.push(row);
  return (
    <div className="mt-5 flex flex-col gap-1">
      {rows.map((items, index) => {
        const sum = items.reduce(
          (value, entry) => value + aspectRatio(entry),
          0,
        );
        const width = index === rows.length - 1 ? Math.min(1, sum / target) : 1;
        return (
          <div
            className="flex gap-1"
            key={items[0].id}
            style={{ width: `${width * 100}%` }}
          >
            {items.map((entry) => (
              <div
                className="min-w-0"
                key={entry.id}
                style={{ flex: `${aspectRatio(entry)} 1 0` }}
              >
                <MediaThumbnail entry={entry} />
              </div>
            ))}
          </div>
        );
      })}
    </div>
  );
}
