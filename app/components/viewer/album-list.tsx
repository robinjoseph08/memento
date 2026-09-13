import { Link } from "react-router-dom";

import { useViewerAlbums } from "../../hooks/queries/viewer";
import { AlbumImage } from "../albums/album-image";
import { countLabel } from "../albums/moment-labels";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { captureRange } from "./labels";

export function ViewerAlbumList() {
  const query = useViewerAlbums();
  return (
    <div className="pt-4 min-[761px]:pt-11">
      <PageTitle title="Albums" />
      <h1 className="font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] tracking-[-1px]">
        Your albums
      </h1>
      {query.isPending && (
        <p className="mt-9 text-muted" role="status">
          Loading albums…
        </p>
      )}
      {query.isError ? (
        <section className="mt-9">
          <p role="alert">Could not load albums. Please try again.</p>
          <Button
            className="mt-3"
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
            variant="outline"
          >
            Try again
          </Button>
        </section>
      ) : query.data?.length === 0 ? (
        <section className="mt-9 border-t border-border py-9">
          <h2 className="font-heading text-[27px]/[1.2]">No albums yet</h2>
          <p className="mt-4 max-w-120 text-muted">
            There are no albums to view yet. Your Curator will choose what to
            share with you.
          </p>
        </section>
      ) : (
        <ul className="mt-9 grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4">
          {query.data?.map((album) => (
            <li className="min-w-0" key={album.id}>
              <Link
                className="block rounded-sm p-2 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                to={`/albums/${encodeURIComponent(album.id)}/photos`}
              >
                <AlbumImage
                  alt={album.title}
                  className="aspect-square w-full object-cover"
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
                  {captureRange(album)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
