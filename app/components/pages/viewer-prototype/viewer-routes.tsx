// PROTOTYPE. Mounts the chosen viewer on the existing /albums routes under
// `pnpm prototype:viewer`. The Timeline and Sheet variants were dropped after
// the approved design was chosen again.
import { useParams } from "react-router-dom";

import { ReadFailure } from "../../people/form-fields";
import { PageTitle } from "../../shell/page-title";
import { PrototypeSwitcher } from "../curator-prototype/switcher";
import { AlbumList, AlbumView } from "./album-viewer";
import { useMemberAlbum, useMemberAlbums } from "./viewer-shared";

export function ViewerPrototype() {
  const { id = "", tab = "", mediaId = "" } = useParams();
  return (
    <>
      {id ? <AlbumRoute id={id} mediaId={mediaId} tab={tab} /> : <ListRoute />}
      <PrototypeSwitcher albumID="lake" name="Viewer" />
    </>
  );
}

function ListRoute() {
  const albums = useMemberAlbums();
  return (
    <>
      <PageTitle title="Albums" />
      {albums.isPending && (
        <p className="px-6 py-10 text-muted" role="status">
          Loading albums…
        </p>
      )}
      {albums.isError && (
        <div className="px-6 py-10">
          <ReadFailure
            error={albums.error}
            pending={albums.isFetching}
            retry={albums.refetch}
          />
        </div>
      )}
      {albums.data && <AlbumList albums={albums.data} />}
    </>
  );
}

function AlbumRoute({
  id,
  tab,
  mediaId,
}: {
  id: string;
  tab: string;
  mediaId: string;
}) {
  const album = useMemberAlbum(id);
  return (
    <>
      <PageTitle title={album.data?.title ?? "Album"} />
      {album.isPending && (
        <p className="px-6 py-10 text-muted" role="status">
          Loading album…
        </p>
      )}
      {album.isError && (
        <div className="px-6 py-10">
          <ReadFailure
            error={album.error}
            pending={album.isFetching}
            retry={album.refetch}
          />
        </div>
      )}
      {album.data && (
        <AlbumView album={album.data} mediaId={mediaId} tab={tab} />
      )}
    </>
  );
}
