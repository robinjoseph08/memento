import { Images } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";

import { useMoreAlbums, useViewerAlbums } from "../../hooks/queries/viewer";
import { useTip } from "../../hooks/use-tip";
import type { ViewerAlbum } from "../../types/generated/publishing";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumCard } from "./album-card";
import { Tip } from "./tip";

export function ViewerAlbumList() {
  const query = useViewerAlbums();
  const more = useMoreAlbums();
  const offered = (more.data?.length ?? 0) > 0;
  // With none of their own, a viewer's page waits for More albums before
  // saying there is nothing to see.
  const empty = query.data?.length === 0;
  // Offered Albums stay out of the viewer's own until they join one, which
  // other photo apps never ask of them.
  const [moreHeading, setMoreHeading] = useState<HTMLHeadingElement | null>(
    null,
  );
  const joinTip = useTip("join", { ready: offered && !!moreHeading });
  return (
    <div className="pt-4 min-[761px]:pt-11">
      <PageTitle title="Albums" />
      <h1 className="font-heading text-[clamp(34px,4vw,48px)] leading-[1.2] tracking-[-1px]">
        Your albums
      </h1>
      <p className="mt-3 text-sm text-muted">
        Here are all the albums that have been shared with you.
      </p>
      {(query.isPending || (empty && more.isPending)) && (
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
      ) : empty ? (
        offered ? (
          <p className="mt-9 text-sm text-muted">
            None yet. You can browse the albums below.
          </p>
        ) : (
          !more.isPending && (
            <EmptyState className="mt-9" icon={Images} title="No albums yet">
              There are no albums to view yet. Your Curator will choose what to
              share with you.
            </EmptyState>
          )
        )
      ) : (
        query.data && (
          <AlbumGrid
            albums={query.data}
            link={(id) => `/albums/${encodeURIComponent(id)}/photos`}
          />
        )
      )}
      {more.isError && (
        <section className="mt-14">
          <p role="alert">Could not load more albums. Please try again.</p>
          <Button
            className="mt-3"
            disabled={more.isFetching}
            onClick={() => void more.refetch()}
            variant="outline"
          >
            Try again
          </Button>
        </section>
      )}
      {offered && more.data && (
        <section aria-labelledby="more-albums" className="mt-14">
          <h2
            className="font-heading text-[clamp(26px,3vw,34px)] leading-[1.2] tracking-[-0.5px]"
            id="more-albums"
            ref={setMoreHeading}
          >
            More albums
          </h2>
          <p className="mt-3 text-sm text-muted">
            These albums are shared with you but aren't in your albums yet. Open
            one to look around, or join it to keep it within your library.
          </p>
          {joinTip.showing && moreHeading && (
            <Tip
              onDone={joinTip.retire}
              target={moreHeading}
              title="Albums you can join"
            >
              You're not in these albums, but you can poke around in them if
              you're interested.
            </Tip>
          )}
          <AlbumGrid
            albums={more.data}
            link={(id) => `/albums/${encodeURIComponent(id)}/preview/photos`}
            offered
          />
        </section>
      )}
    </div>
  );
}

function AlbumGrid({
  albums,
  link,
  offered = false,
}: {
  albums: ViewerAlbum[];
  link: (id: string) => string;
  offered?: boolean;
}) {
  return (
    <ul className="mt-9 grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4">
      {albums.map((album) => (
        <li className="min-w-0" key={album.id}>
          <Link
            className="group block rounded-sm p-2 focus-visible:outline-2 focus-visible:outline-ring"
            to={link(album.id)}
          >
            <AlbumCard album={album} offered={offered} />
          </Link>
        </li>
      ))}
    </ul>
  );
}
