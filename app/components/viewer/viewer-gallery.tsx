import { Image, Play, SquarePlay } from "lucide-react";
import { useRef, type ReactNode } from "react";
import { Link, type To } from "react-router-dom";

import {
  dayCount,
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
import { countLabel } from "../albums/moment-labels";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumHeader } from "./album-header";
import { aspectRatio, captureDate } from "./labels";
import { Lightbox } from "./lightbox";
import { RequestAccess } from "./request-access";
import { Timeline } from "./timeline";

// The shared Album presentation for ordinary viewing and Curator preview.
// personName is set only in preview so empty states can say whose view it is.
// entryID names the item open in the lightbox on the current tab; entryLink
// builds each item's stable URL in whatever form the surrounding route uses.
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
  // Every run of days loads at once, and each day keeps its place from the
  // first paint with a heading, its count, and a block the size its media
  // should take, so the page height and the timeline are settled before any
  // entry arrives. Image bytes still load lazily as rows scroll into view.
  const runs = useViewerEntries(context, tab, album.days);
  const sectionsRef = useRef<HTMLDivElement>(null);
  const failure = runs.find((run) => run.result.error)?.result.error;
  if (
    failure instanceof HTTPError &&
    (failure.status === 403 || failure.status === 404)
  ) {
    return (
      <p className="py-10 text-muted" role="alert">
        {personName
          ? `This album is no longer available to ${personName}.`
          : "This album is no longer available to you."}
      </p>
    );
  }
  const pending = runs.some((run) => run.result.isPending);
  const fetching = runs.some((run) => run.result.isFetching);
  const retry = () => {
    for (const { result } of runs) if (result.isError) void result.refetch();
  };
  // The lightbox walks neighbours, so it only sees the runs loaded in order
  // from the start; later runs join once the gap before them fills.
  const loaded: ViewerEntry[] = [];
  for (const { result } of runs) {
    if (!result.isSuccess) break;
    loaded.push(...result.data);
  }
  const other = tab === "photos" ? "videos" : "photos";
  return (
    <>
      {pending && (
        <p className="sr-only" role="status">
          Loading {tab}…
        </p>
      )}
      {failure && (
        <div className="py-8">
          <p role="alert">Could not load {tab}. Please try again.</p>
          <Button
            className="mt-3"
            disabled={fetching}
            onClick={retry}
            variant="outline"
          >
            Try again
          </Button>
        </div>
      )}
      {runs.length === 0 &&
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
      <div className="@container" ref={sectionsRef}>
        {runs.map((run) =>
          run.days.map((day) => {
            const items = run.result.data?.filter(
              (entry) =>
                entry.captured_at.slice(0, 10) === day.date.slice(0, 10),
            );
            if (items && items.length === 0) return null;
            const count = dayCount(day, tab);
            return (
              <section
                aria-label={captureDate(day.date, true)}
                className="pt-10"
                data-date={day.date.slice(0, 10)}
                key={day.date}
              >
                <h2 className="font-heading text-[27px]/[1.2] tracking-[-0.35px]">
                  {captureDate(day.date, true)}{" "}
                  <span className="ml-3 font-sans text-xs tracking-normal whitespace-nowrap text-muted">
                    {tab === "photos"
                      ? countLabel(count, "photo", "photos")
                      : countLabel(count, "video", "videos")}
                  </span>
                </h2>
                {tab === "photos" ? (
                  items ? (
                    <PhotoRows
                      ratios={items.map(aspectRatio)}
                      tile={(index) => (
                        <PhotoTile entry={items[index]} entryLink={entryLink} />
                      )}
                    />
                  ) : (
                    <PhotoRows
                      ratios={day.photo_ratios}
                      tile={(index) => (
                        <div
                          aria-hidden="true"
                          className="rounded-[2px] bg-surface"
                          style={{ aspectRatio: day.photo_ratios[index] }}
                        />
                      )}
                    />
                  )
                ) : !items ? (
                  <VideoPlaceholder count={count} />
                ) : (
                  <ul
                    aria-label="Videos"
                    className="mt-5 grid grid-cols-1 gap-x-4 gap-y-6 min-[601px]:grid-cols-2 min-[1001px]:grid-cols-3"
                  >
                    {items.map((entry) => (
                      <li key={entry.id}>
                        <Link
                          aria-label={`Open video ${entry.title || "Video"}`}
                          className="block rounded-[2px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                          data-entry-id={entry.id}
                          state={{ origin: entry.id }}
                          to={entryLink(entry.id)}
                        >
                          <span className="relative block overflow-hidden rounded-[2px] bg-surface">
                            <MediaThumbnail entry={entry} />
                            {entry.available && (
                              <PlayBadge className="inset-0 m-auto size-12" />
                            )}
                          </span>
                          <h3 className="mt-3 font-heading text-lg leading-tight">
                            {entry.title}
                          </h3>
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            );
          }),
        )}
      </div>
      {/* The Curator preview is a pane inside the editor, not its own scrolling
          page, so the rail that stands in for the page scrollbar stays out. */}
      {!personName && runs.length > 0 && (
        <Timeline key={tab} sectionsRef={sectionsRef} />
      )}
      {entryID && (
        <Lightbox
          closeTo={tabLinks[tab]}
          currentID={entryID}
          entries={loaded}
          entryLink={entryLink}
          kind={tab === "photos" ? "photo" : "video"}
          loading={pending}
          personName={personName}
          retry={failure ? retry : undefined}
          title={album.title}
          total={tab === "photos" ? album.photo_count : album.video_count}
        />
      )}
    </>
  );
}

// The gallery fits photos into rows of the target ratio, and videos into as
// many columns.
function useRowLayout() {
  const desktop = useMediaQuery("(min-width: 1001px)");
  const tablet = useMediaQuery("(min-width: 601px)");
  return desktop
    ? { target: 4.5, columns: 3 }
    : tablet
      ? { target: 3, columns: 2 }
      : { target: 1.5, columns: 1 };
}

// Holds a day's videos' height while they load, assuming 16:9 tiles with a
// title.
function VideoPlaceholder({ count }: { count: number }) {
  const { columns } = useRowLayout();
  const rows = Math.ceil(count / columns);
  return (
    <div
      aria-hidden="true"
      className="mt-5 rounded-[2px] bg-surface"
      style={{ aspectRatio: `${columns * 16} / ${rows * 10.5}` }}
    />
  );
}

// A neutral video marker on gallery tiles. Playback lives in the lightbox.
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
        alt={entry.title || (entry.kind === "VIDEO" ? "Video" : "Photo")}
        className="h-full w-full"
        fallback="Media unavailable"
        src={entry.available ? entry.preview_url : ""}
      />
    </div>
  );
}

// Rows that preserve every aspect ratio: items join a row until its combined
// width-to-height ratio would exceed the target, then each item's ratio is its
// flex share, normalised so a lone portrait still fills its row. Short rows
// keep their natural size instead of stretching. The
// same ratios lay out a day before its photos arrive, so nothing moves when
// they do. Rows off screen skip layout and paint; their height is declared
// from the same arithmetic in container units, gaps included, so the page
// height is exact before they render.
function PhotoRows({
  ratios,
  tile,
}: {
  ratios: number[];
  tile: (index: number) => ReactNode;
}) {
  const { target } = useRowLayout();
  const rows: { items: number[]; sum: number; share: number }[] = [];
  ratios.forEach((ratio, index) => {
    const last = rows.at(-1);
    if (!last || last.sum + ratio > target + 0.01)
      rows.push({ items: [index], sum: ratio, share: 1 });
    else {
      last.items.push(index);
      last.sum += ratio;
    }
  });
  for (const [index, row] of rows.entries())
    if (index === rows.length - 1 || row.sum < target * 0.7)
      row.share = Math.min(1, row.sum / target);
  const height = rows
    .map(
      (row) =>
        `(100cqw * ${row.share} - ${(row.items.length - 1) * 4}px) / ${row.sum}`,
    )
    .concat(`${Math.max(0, rows.length - 1) * 4}px`)
    .join(" + ");
  return (
    <div
      className="mt-5 flex flex-col gap-1"
      style={{
        contentVisibility: "auto",
        containIntrinsicHeight: `auto calc(${height})`,
      }}
    >
      {rows.map((row) => (
        <div
          className="flex gap-1"
          key={row.items[0]}
          style={{ width: `${row.share * 100}%` }}
        >
          {row.items.map((index) => (
            <div
              className="min-w-0"
              key={index}
              style={{ flex: `${ratios[index] / row.sum} 1 0` }}
            >
              {tile(index)}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// Each photo is a link to its stable URL; the link remembers that it opened
// the lightbox so closing can return focus here.
function PhotoTile({
  entry,
  entryLink,
}: {
  entry: ViewerEntry;
  entryLink: (id: string) => To;
}) {
  return (
    <Link
      aria-label={`Open photo ${entry.title || "Photo"}`}
      className="block rounded-[2px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      data-entry-id={entry.id}
      state={{ origin: entry.id }}
      to={entryLink(entry.id)}
    >
      <MediaThumbnail entry={entry} />
    </Link>
  );
}
