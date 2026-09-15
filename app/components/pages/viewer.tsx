import { Navigate, useParams } from "react-router-dom";

import type { ViewerTab } from "../../hooks/queries/viewer";
import { BackLink } from "../shell/back-link";
import { PageTitle } from "../shell/page-title";
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
  return (
    <>
      <BackLink className="min-[761px]:mb-10" to="/albums">
        All albums
      </BackLink>
      <ViewerGallery
        context={{ albumID: id }}
        entryID={entryID}
        entryLink={(entry) => `${base}/${tab}/${encodeURIComponent(entry)}`}
        tab={tab}
        tabLinks={{ photos: `${base}/photos`, videos: `${base}/videos` }}
      />
    </>
  );
}
