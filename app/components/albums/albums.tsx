import { Link, useSearchParams } from "react-router-dom";

import { useAlbums } from "../../hooks/queries/albums";
import { SearchForm } from "../forms/search-form";
import {
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";

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

const importLabels: Record<string, string> = {
  queued: "Waiting to import",
  processing: "Import in progress",
  interrupted: "Import interrupted",
  failed: "Import failed",
};

export function CuratorPage() {
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
          <ul className="divide-y divide-border border-y border-border">
            {albums.data.map((album) => (
              <li key={album.id}>
                <Link
                  className="flex cursor-pointer items-center gap-3 px-3 py-3 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                  to={`/curator/albums/${album.id}`}
                >
                  <AlbumImage
                    alt={album.title}
                    className="h-auto max-h-20 w-auto max-w-28 shrink-0"
                    fallback="No cover available"
                    src={album.cover_url}
                  />
                  <span className="min-w-0">
                    <span className="block wrap-anywhere">{album.title}</span>
                    <span className="text-xs text-muted">
                      {album.status === "complete"
                        ? `${album.photo_count} ${album.photo_count === 1 ? "photo" : "photos"}, ${album.video_count} ${album.video_count === 1 ? "video" : "videos"}`
                        : (importLabels[album.status] ??
                          "Import status unavailable")}
                      {!album.published && ", unpublished"}
                    </span>
                    {album.status === "complete" &&
                      (album.start_date || album.end_date) && (
                        <span className="block text-xs text-muted">
                          {[
                            dateLabel(album.start_date),
                            dateLabel(album.end_date),
                          ]
                            .filter(Boolean)
                            .filter(
                              (date, index, dates) =>
                                dates.indexOf(date) === index,
                            )
                            .join(" to ")}
                        </span>
                      )}
                  </span>
                </Link>
              </li>
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
