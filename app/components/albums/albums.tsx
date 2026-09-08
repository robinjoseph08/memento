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
      <div className="mb-9 flex flex-wrap items-start justify-between gap-6">
        <div>
          <h1 className={headingClass}>Your albums</h1>
          <p className="mt-5 max-w-150 text-muted">
            Choose the photos and videos you want to share with friends and
            family.
          </p>
        </div>
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
          <ul className="divide-y divide-border border-t border-border">
            {albums.data.map((album) => (
              <li key={album.id}>
                <Link
                  className="flex flex-wrap items-center justify-between gap-4 rounded-sm py-6 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                  to={`/curator/albums/${album.id}`}
                >
                  <h2 className={sectionHeadingClass}>{album.title}</h2>
                  <span className="text-sm text-muted">
                    {album.status === "complete"
                      ? `${album.total} items`
                      : (importLabels[album.status] ??
                        "Import status unavailable")}
                    {!album.published && ", unpublished"}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <section className="border-t border-border py-9">
            <h2 className={sectionHeadingClass}>No albums yet</h2>
            <p className="mt-4 max-w-120 text-muted">
              Import an album from Immich to start organizing your photos and
              videos.
            </p>
          </section>
        ))}
    </>
  );
}
