import { Image, Play, SquarePlay } from "lucide-react";
import { useEffect } from "react";
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
import { MediaCount } from "../albums/media-counts";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumHeader } from "./album-header";
import { aspectRatio, captureDate } from "./labels";
import { Lightbox } from "./lightbox";
import { RequestAccess } from "./request-access";

// The shared Album presentation for ordinary viewing and Curator preview.
// personName is set only in preview so empty states can say whose view it is.
// entryID names the photo open in the lightbox; entryLink builds each photo's
// stable URL in whatever form the surrounding route uses.
export function ViewerGallery({
  context,
  tab,
  tabLinks,
  personName,
  entryID,
  entryLink,
}: {
  context: ViewerContext;
  tab: ViewerTab;
  tabLinks: Record<ViewerTab, To>;
  personName?: string;
  entryID?: string;
  entryLink: (id: string) => To;
}) {
  const query = useViewerAlbum(context);
  const noAccess =
    query.error instanceof HTTPError && query.error.status === 404;
  const tabs = [
    { key: "photos", label: "Photos", icon: Image },
    { key: "videos", label: "Videos", icon: SquarePlay },
  ] as const;
  return (
    <>
      <PageTitle
        title={query.data?.title ?? (personName ? "Viewer preview" : "Album")}
      />
      {query.isPending ? (
        <p role="status">
          {personName ? "Building the preview…" : "Loading album…"}
        </p>
      ) : query.isError ? (
        <section className="py-10">
          <h1 className="font-heading text-3xl">
            {noAccess ? "Album not available" : "Could not load album"}
          </h1>
          <p className="mt-3 text-muted">
            {noAccess
              ? personName
                ? `${personName} has no access to this album.`
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
          {noAccess && !personName && (
            <RequestAccess albumID={context.albumID} />
          )}
        </section>
      ) : (
        <>
          <AlbumHeader
            album={query.data}
            coverFallback={
              personName ? `No cover is visible to ${personName}` : "No cover"
            }
          />
          <nav
            aria-label="Album media"
            className="mt-10 flex border-b border-border min-[761px]:mt-14"
          >
            {tabs.map((item) => (
              <Link
                aria-current={tab === item.key ? "page" : undefined}
                className={cn(
                  "-mb-px inline-flex items-center gap-2 border-b-2 px-[18px] py-3 text-sm",
                  tab === item.key
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted hover:text-foreground",
                )}
                key={item.key}
                to={tabLinks[item.key]}
              >
                <item.icon
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
                {item.label}{" "}
                <span
                  className={cn(
                    "rounded-sm px-1.5 text-xs",
                    tab === item.key
                      ? "bg-primary/15 text-accent-foreground"
                      : "bg-surface",
                  )}
                >
                  {item.key === "photos"
                    ? query.data.photo_count
                    : query.data.video_count}
                </span>
              </Link>
            ))}
          </nav>
          <GalleryEntries
            album={query.data}
            context={context}
            entryID={entryID}
            entryLink={entryLink}
            personName={personName}
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
  personName,
  entryID,
  entryLink,
}: {
  album: ViewerAlbum;
  context: ViewerContext;
  tab: ViewerTab;
  tabLinks: Record<ViewerTab, To>;
  personName?: string;
  entryID?: string;
  entryLink: (id: string) => To;
}) {
  const query = useViewerEntries(context, tab);
  // Pages arrive one after another in the background until the gallery is
  // complete, so the scrollbar and every day heading reflect the whole Album
  // without a click. Image bytes still load lazily as rows scroll into view.
  // The page count is a dependency because a slow renderer can receive the
  // next page before it ever renders the fetching state, and the chain must
  // continue from each arrival rather than from that transient flag.
  const {
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    fetchNextPage,
  } = query;
  const pageCount = query.data?.pages.length ?? 0;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && !isFetchNextPageError)
      void fetchNextPage();
  }, [
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    fetchNextPage,
    pageCount,
  ]);
  if (
    query.error instanceof HTTPError &&
    (query.error.status === 403 || query.error.status === 404)
  ) {
    return (
      <p className="py-10 text-muted" role="alert">
        {personName
          ? `This album is no longer available to ${personName}.`
          : "This album is no longer available to you."}
      </p>
    );
  }
  const entries = query.data?.pages.flatMap((page) => page.entries) ?? [];
  const other = tab === "photos" ? "videos" : "photos";
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
      {query.isSuccess &&
        entries.length === 0 &&
        (personName ? (
          <p className="py-8 text-sm text-muted">
            No {tab} are shared with {personName} yet.
          </p>
        ) : (
          <section className="py-14">
            <h2 className="font-heading text-[27px]/[1.2]">
              No {tab} in this album
            </h2>
            <p className="mt-3 max-w-100 text-sm text-muted">
              {tab === "videos"
                ? "You can see this album's photos in the Photos tab."
                : "You can watch this album's videos in the Videos tab."}
            </p>
            <Link
              className="mt-6 inline-flex min-h-11 items-center rounded-md border border-border px-4 text-sm hover:bg-surface"
              to={tabLinks[other]}
            >
              {other === "photos" ? "View photos" : "Watch videos"}
            </Link>
          </section>
        ))}
      {album.days.map((day) => {
        const items = entries.filter(
          (entry) => entry.captured_at.slice(0, 10) === day.date.slice(0, 10),
        );
        if (!items.length) return null;
        const count = tab === "photos" ? day.photo_count : day.video_count;
        return (
          <section
            aria-label={captureDate(day.date, true)}
            className="pt-10"
            key={day.date}
          >
            <h2 className="font-heading text-[27px]/[1.2] tracking-[-0.35px]">
              {captureDate(day.date, true)}{" "}
              <span className="ml-3 font-sans text-xs tracking-normal whitespace-nowrap text-muted">
                <MediaCount count={count} kind={tab} />
              </span>
            </h2>
            {tab === "photos" ? (
              <PhotoRows entries={items} entryLink={entryLink} />
            ) : (
              <ul
                aria-label="Videos"
                className="mt-5 grid grid-cols-1 gap-x-4 gap-y-6 min-[601px]:grid-cols-2 min-[1001px]:grid-cols-3"
              >
                {items.map((entry) => (
                  <li key={entry.id}>
                    <span className="relative block overflow-hidden rounded-[2px] bg-surface">
                      <MediaThumbnail entry={entry} />
                      {entry.available && (
                        <PlayBadge className="inset-0 m-auto size-12" />
                      )}
                    </span>
                    <h3 className="mt-3 font-heading text-lg leading-tight">
                      {entry.title}
                    </h3>
                  </li>
                ))}
              </ul>
            )}
          </section>
        );
      })}
      {query.isFetchingNextPage && (
        <p className="mt-8 text-sm text-muted" role="status">
          Loading more {tab}…
        </p>
      )}
      {tab === "photos" && entryID && (query.data || !query.isError) && (
        <Lightbox
          closeTo={tabLinks.photos}
          currentID={entryID}
          entries={entries}
          entryLink={entryLink}
          loading={
            query.isPending ||
            isFetchingNextPage ||
            (!!hasNextPage && !isFetchNextPageError)
          }
          personName={personName}
          retry={isFetchNextPageError ? () => void fetchNextPage() : undefined}
          title={album.title}
          total={album.photo_count}
        />
      )}
    </>
  );
}

// A neutral video marker. Playback arrives with its own controls later.
function PlayBadge({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "pointer-events-none absolute flex size-10 items-center justify-center rounded-full bg-black/60 text-white",
        className,
      )}
    >
      <Play className="ml-0.5 size-5" fill="currentColor" strokeWidth={0} />
    </span>
  );
}

function MediaThumbnail({ entry }: { entry: ViewerEntry }) {
  return (
    <div style={{ aspectRatio: aspectRatio(entry) }}>
      <AlbumImage
        alt={entry.title || "Photo"}
        className="h-full w-full"
        fallback="Media unavailable"
        src={entry.available ? entry.preview_url : ""}
      />
    </div>
  );
}

// Rows that preserve every aspect ratio: items join a row until its combined
// width-to-height ratio would exceed the target, then each item's ratio is its
// flex share. Short rows keep their natural size instead of stretching.
// Each photo is a link to its stable URL; the link remembers that it opened
// the lightbox so closing can return focus here.
function PhotoRows({
  entries,
  entryLink,
}: {
  entries: ViewerEntry[];
  entryLink: (id: string) => To;
}) {
  const desktop = useMediaQuery("(min-width: 1001px)");
  const tablet = useMediaQuery("(min-width: 601px)");
  const target = desktop ? 4.5 : tablet ? 3 : 1.5;
  const rows: ViewerEntry[][] = [];
  for (const entry of entries) {
    const last = rows.at(-1);
    const sum =
      last?.reduce((total, item) => total + aspectRatio(item), 0) ?? 0;
    if (!last || sum + aspectRatio(entry) > target + 0.01) rows.push([entry]);
    else last.push(entry);
  }
  return (
    <div className="mt-5 flex flex-col gap-1">
      {rows.map((items, index) => {
        const sum = items.reduce(
          (value, entry) => value + aspectRatio(entry),
          0,
        );
        const natural = index === rows.length - 1 || sum < target * 0.7;
        return (
          <div
            className="flex gap-1"
            key={items[0].id}
            style={{
              width: `${(natural ? Math.min(1, sum / target) : 1) * 100}%`,
            }}
          >
            {items.map((entry) => (
              <div
                className="min-w-0"
                key={entry.id}
                style={{ flex: `${aspectRatio(entry)} 1 0` }}
              >
                <Link
                  aria-label={`Open photo ${entry.title || "Photo"}`}
                  className="block rounded-[2px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  data-entry-id={entry.id}
                  state={{ origin: entry.id }}
                  to={entryLink(entry.id)}
                >
                  <MediaThumbnail entry={entry} />
                </Link>
              </div>
            ))}
          </div>
        );
      })}
    </div>
  );
}
