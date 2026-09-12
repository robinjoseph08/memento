// PROTOTYPE. Three variants of the member-facing viewer on the existing
// /albums routes, switchable with ?variant=A|B|C.
//   A  Approved: the reference design rebuilt on the real shell. Big left
//      heading, plain cover at the right, Photos and Videos tabs, justified
//      rows under day headings.
//   B  Timeline: one chronological stream of photos and videos with a date
//      rail, a filter instead of tabs, and no separate cover.
//   C  Sheet: a centered cover hero and title, pill tabs, and a uniform
//      square grid that fits the most media on screen.
import { useParams, useSearchParams } from "react-router-dom";

import { ReadFailure } from "../../people/form-fields";
import { PageTitle } from "../../shell/page-title";
import { PrototypeSwitcher } from "../curator-prototype/switcher";
import { AlbumA, AlbumListA } from "./variant-a";
import { AlbumB, AlbumListB } from "./variant-b";
import { AlbumC, AlbumListC } from "./variant-c";
import { useMemberAlbum, useMemberAlbums } from "./viewer-shared";

const variants = [
  { key: "A", name: "Approved" },
  { key: "B", name: "Timeline" },
  { key: "C", name: "Sheet" },
];

export function useVariantLink() {
  const [params] = useSearchParams();
  const kept = new URLSearchParams();
  const variant = params.get("variant");
  if (variant) kept.set("variant", variant);
  const suffix = kept.size ? `?${kept}` : "";
  return (path: string) => `${path}${suffix}`;
}

export function ViewerVariants() {
  const [params] = useSearchParams();
  const { id = "", tab = "", mediaId = "" } = useParams();
  const variant =
    variants.find((item) => item.key === params.get("variant"))?.key ?? "A";
  return (
    <>
      {id ? (
        <AlbumRoute id={id} mediaId={mediaId} tab={tab} variant={variant} />
      ) : (
        <ListRoute variant={variant} />
      )}
      <PrototypeSwitcher
        albumID="lake"
        current={variant}
        name="Viewer"
        variants={variants}
      />
    </>
  );
}

function ListRoute({ variant }: { variant: string }) {
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
      {albums.data &&
        (variant === "B" ? (
          <AlbumListB albums={albums.data} />
        ) : variant === "C" ? (
          <AlbumListC albums={albums.data} />
        ) : (
          <AlbumListA albums={albums.data} />
        ))}
    </>
  );
}

function AlbumRoute({
  id,
  tab,
  mediaId,
  variant,
}: {
  id: string;
  tab: string;
  mediaId: string;
  variant: string;
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
      {album.data &&
        (variant === "B" ? (
          <AlbumB album={album.data} mediaId={mediaId} tab={tab} />
        ) : variant === "C" ? (
          <AlbumC album={album.data} mediaId={mediaId} tab={tab} />
        ) : (
          <AlbumA album={album.data} mediaId={mediaId} tab={tab} />
        ))}
    </>
  );
}
