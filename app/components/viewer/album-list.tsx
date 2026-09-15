import { Images } from "lucide-react";
import { Link } from "react-router-dom";

import { useViewerAlbums } from "../../hooks/queries/viewer";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumCard } from "./album-card";

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
        <EmptyState className="mt-9" icon={Images} title="No albums yet">
          There are no albums to view yet. Your Curator will choose what to
          share with you.
        </EmptyState>
      ) : (
        <ul className="mt-9 grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4">
          {query.data?.map((album) => (
            <li className="min-w-0" key={album.id}>
              <Link
                className="group block rounded-sm p-2 focus-visible:outline-2 focus-visible:outline-ring"
                to={`/albums/${encodeURIComponent(album.id)}/photos`}
              >
                <AlbumCard album={album} />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
