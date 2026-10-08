import { Navigate, useParams } from "react-router-dom";

import type { ViewerTab } from "../../hooks/queries/viewer";
import { BackLink } from "../shell/back-link";
import { PageTitle } from "../shell/page-title";
import {
  JoinAlbum,
  LeaveAlbum,
  MoreAvailable,
} from "../viewer/album-membership";
import { ViewerGallery } from "../viewer/viewer-gallery";

export function ViewerLibraryPage({ tab }: { tab: ViewerTab }) {
  const { entryID } = useParams();
  return (
    <div className="pt-4 min-[761px]:pt-11">
      <ViewerGallery
        context={{}}
        entryID={entryID}
        entryLink={(entry) => `/library/${tab}/${encodeURIComponent(entry)}`}
        tab={tab}
        tabLinks={{ photos: "/library/photos", videos: "/library/videos" }}
      />
    </div>
  );
}

export function ViewerLibraryRedirect() {
  return (
    <>
      <PageTitle title="Library" />
      <Navigate replace to="/library/photos" />
    </>
  );
}

export function ViewerAlbumRedirect() {
  const { id = "" } = useParams();
  return (
    <>
      <PageTitle title="Album" />
      <Navigate replace to={`/albums/${encodeURIComponent(id)}/photos`} />
    </>
  );
}

export function ViewerAlbumPage({ tab }: { tab: ViewerTab }) {
  const { id = "", entryID } = useParams();
  const base = `/albums/${encodeURIComponent(id)}`;
  const opened = entryID ? `/${encodeURIComponent(entryID)}` : "";
  return (
    <>
      <BackLink className="min-[761px]:mb-10" to="/albums">
        All albums
      </BackLink>
      <ViewerGallery
        actions={(album) => album.joined && <LeaveAlbum albumID={id} />}
        context={{ albumID: id }}
        entryID={entryID}
        entryLink={(entry) => `${base}/${tab}/${encodeURIComponent(entry)}`}
        fallback={{
          context: { albumID: id, offered: true },
          to: `${base}/preview/${tab}${opened}`,
        }}
        footer={(album) => <MoreAvailable album={album} tab={tab} />}
        tab={tab}
        tabLinks={{ photos: `${base}/photos`, videos: `${base}/videos` }}
      />
    </>
  );
}

export function ViewerOfferedAlbumRedirect() {
  const { id = "" } = useParams();
  return (
    <>
      <PageTitle title="Album" />
      <Navigate
        replace
        to={`/albums/${encodeURIComponent(id)}/preview/photos`}
      />
    </>
  );
}

// An Album offered to the viewer, browsed apart from their own Albums as it
// would look once joined. A shared link reaching someone with nothing offered
// but the Album of their own, or who already joined it, opens their own copy.
export function ViewerOfferedAlbumPage({ tab }: { tab: ViewerTab }) {
  const { id = "", entryID } = useParams();
  const own = `/albums/${encodeURIComponent(id)}`;
  const base = `${own}/preview`;
  const opened = entryID ? `/${encodeURIComponent(entryID)}` : "";
  return (
    <>
      <BackLink className="min-[761px]:mb-10" to="/albums">
        All albums
      </BackLink>
      <ViewerGallery
        actions={() => <JoinAlbum albumID={id} tab={tab} />}
        context={{ albumID: id, offered: true }}
        entryID={entryID}
        entryLink={(entry) => `${base}/${tab}/${encodeURIComponent(entry)}`}
        fallback={{
          context: { albumID: id },
          to: `${own}/${tab}${opened}`,
        }}
        tab={tab}
        tabLinks={{ photos: `${base}/photos`, videos: `${base}/videos` }}
      />
    </>
  );
}
