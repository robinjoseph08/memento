import { Link, useSearchParams } from "react-router-dom";

import { useAlbums, useRetryAlbum } from "../../hooks/queries/albums";
import type { Album } from "../../types/generated/publishing";
import { SearchForm } from "../forms/search-form";
import {
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";
import {
  albumState,
  importLabels,
  stateLabels,
  type AlbumState,
} from "./album-state";
import { MediaCounts } from "./media-counts";

function dateLabel(value: string) {
  const date = new Date(`${value.slice(0, 10)}T12:00:00`);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        year: "numeric",
      });
}

export function CuratorAlbumsPage() {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const albums = useAlbums(search);
  return (
    <>
      <PageTitle title="Albums" />
      <div className="mb-9 flex flex-wrap items-center justify-between gap-5">
        <h1 className={headingClass}>Your albums</h1>
        <Button asChild>
          <Link to="/curator/import">Import an album</Link>
        </Button>
      </div>
      <SearchForm
        className="mb-7"
        label="Search albums"
        onSearch={(value) => setParams(value.trim() ? { q: value.trim() } : {})}
        value={search}
      />
      {albums.isPending && <p role="status">Loading albums…</p>}
      {albums.isError && (
        <ReadFailure
          error={albums.error}
          pending={albums.isFetching}
          retry={albums.refetch}
        />
      )}
      {albums.data &&
        (albums.data.length ? (
          <ul className="grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4 min-[1401px]:grid-cols-6">
            {albums.data.map((album) => (
              <AlbumCard album={album} key={album.id} />
            ))}
          </ul>
        ) : (
          <section className="border-t border-border py-8">
            <h2 className={sectionHeadingClass}>
              {search.trim() ? "No matching albums" : "No albums yet"}
            </h2>
            <p className="mt-3 max-w-120 text-muted">
              {search.trim()
                ? "Try another search."
                : "Import an album from Immich to start organizing your photos and videos."}
            </p>
          </section>
        ))}
    </>
  );
}

function AlbumCard({ album }: { album: Album }) {
  const state = albumState(album);
  const dates = [dateLabel(album.start_date), dateLabel(album.end_date)]
    .filter(Boolean)
    .filter((date, index, all) => all.indexOf(date) === index)
    .join(" to ");
  return (
    <li className="min-w-0" data-album-state={state}>
      <Link
        className="block cursor-pointer rounded-sm p-2 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
        to={`/curator/albums/${album.id}`}
      >
        <AlbumImage
          alt={album.title}
          className="aspect-square h-auto w-full object-cover"
          fallback="No cover available"
          src={album.cover_url}
        />
        <span className="mt-3 block min-w-0">
          <span className="block font-heading text-lg wrap-anywhere">
            {album.title}
          </span>
          {album.status === "complete" && dates && (
            <span className="mt-1 block text-xs/5 text-muted">{dates}</span>
          )}
          {album.status === "complete" && (
            <MediaCounts
              className="flex text-xs/5 text-muted"
              photos={album.photo_count}
              videos={album.video_count}
            />
          )}
          <span
            className={`block text-xs/5 ${state === "failed" ? "text-destructive" : "text-muted"}`}
          >
            {importLabels[album.status] ?? stateLabels[state]}
          </span>
        </span>
      </Link>
      <CardAction album={album} state={state} />
    </li>
  );
}

// The direct action for each state sits outside the card link, so it is
// never a control nested inside another control.
function CardAction({ album, state }: { album: Album; state: AlbumState }) {
  const retry = useRetryAlbum(album.id);
  const linkClass =
    "inline-flex min-h-8 items-center rounded-md px-2 text-xs text-accent-foreground underline underline-offset-4 hover:bg-surface";
  switch (state) {
    case "failed":
      return (
        <Form
          aria-busy={retry.isPending}
          aria-label={`Retry import of ${album.title}`}
          className="px-2"
          error={retry.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!retry.isPending) retry.mutate();
          }}
        >
          <Button
            disabled={retry.isPending}
            size="sm"
            type="submit"
            variant="outline"
          >
            {retry.isPending ? "Retrying…" : "Retry import"}
          </Button>
        </Form>
      );
    case "unpublished":
      return (
        <Link
          className={linkClass}
          to={`/curator/albums/${album.id}?section=access`}
        >
          Set up access
        </Link>
      );
    case "ready":
      return (
        <Link className={linkClass} to={`/curator/albums/${album.id}`}>
          Review and publish
        </Link>
      );
    default:
      return null;
  }
}
