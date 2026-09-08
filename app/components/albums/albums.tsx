import { Link } from "react-router-dom";

import { useAlbums } from "../../hooks/queries/albums";
import {
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";

const importLabels: Record<string, string> = {
  queued: "Waiting to import",
  processing: "Import in progress",
  interrupted: "Import interrupted",
  failed: "Import failed",
};

export function CuratorPage() {
  const albums = useAlbums();
  return (
    <>
      <PageTitle title="Albums" />
      <div className="mb-9 flex flex-wrap items-center justify-between gap-5">
        <h1 className={headingClass}>Your albums</h1>
        <Button asChild>
          <Link to="/curator/import">Import an album</Link>
        </Button>
      </div>
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
                  <span
                    aria-hidden="true"
                    className="flex size-9 shrink-0 items-center justify-center rounded-sm bg-surface text-muted"
                  >
                    <svg
                      className="size-5"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      viewBox="0 0 24 24"
                    >
                      <rect height="16" rx="2" width="18" x="3" y="4" />
                      <path d="m3 16 5-5 4 4 3-3 6 6" />
                      <circle cx="16" cy="9" r="1" />
                    </svg>
                  </span>
                  <span className="min-w-0">
                    <span className="block wrap-anywhere">{album.title}</span>
                    <span className="text-xs text-muted">
                      {album.status === "complete"
                        ? `${album.total} items`
                        : (importLabels[album.status] ??
                          "Import status unavailable")}
                      {!album.published && ", unpublished"}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <section className="border-t border-border py-8">
            <h2 className={sectionHeadingClass}>No albums yet</h2>
            <p className="mt-3 max-w-120 text-muted">
              Import an album from Immich to start organizing your photos and
              videos.
            </p>
          </section>
        ))}
    </>
  );
}
