import { ChevronLeft } from "lucide-react";
import { Link, Navigate, useParams } from "react-router-dom";

import type { ViewerTab } from "../../hooks/queries/viewer";
import { PageTitle } from "../shell/page-title";
import { ViewerGallery } from "../viewer/viewer-gallery";

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
  const { id = "" } = useParams();
  const base = `/albums/${encodeURIComponent(id)}`;
  return (
    <>
      <Link
        className="mb-6 -ml-2 inline-flex min-h-9 items-center gap-1 rounded-md px-2 text-sm text-muted hover:bg-surface hover:text-foreground min-[761px]:mb-10"
        to="/albums"
      >
        <ChevronLeft aria-hidden="true" className="size-4" strokeWidth={1.5} />
        All albums
      </Link>
      <ViewerGallery
        context={{ albumID: id }}
        tab={tab}
        tabLinks={{ photos: `${base}/photos`, videos: `${base}/videos` }}
      />
    </>
  );
}
