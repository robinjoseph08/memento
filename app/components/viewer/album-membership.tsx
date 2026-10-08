import { Link, useNavigate } from "react-router-dom";

import {
  useJoinAlbum,
  useLeaveAlbum,
  type ViewerTab,
} from "../../hooks/queries/viewer";
import { errorMessage } from "../../lib/http";
import type { ViewerAlbum } from "../../types/generated/publishing";
import { ConfirmAction } from "../forms/confirm-action";
import { Button } from "../ui/button";

// Join on an offered Album's preview. Joining opens the viewer's own copy of
// the Album, which now holds everything the preview showed.
export function JoinAlbum({
  albumID,
  tab,
}: {
  albumID: string;
  tab: ViewerTab;
}) {
  const join = useJoinAlbum(albumID);
  const navigate = useNavigate();
  return (
    <div className="mt-8 flex flex-wrap items-center gap-x-4 gap-y-3">
      <Button
        disabled={join.isPending}
        onClick={() =>
          join.mutate(undefined, {
            onSuccess: () =>
              void navigate(`/albums/${encodeURIComponent(albumID)}/${tab}`, {
                replace: true,
              }),
          })
        }
      >
        {join.isPending ? "Joining…" : "Join album"}
      </Button>
      <p className="text-sm text-muted">
        Joining adds this album to your albums and its photos and videos to your
        library.
      </p>
      {join.isError && (
        <p className="w-full text-sm text-destructive" role="alert">
          {errorMessage(join.error)}
        </p>
      )}
    </div>
  );
}

// Leave on an Album the viewer joined. Whatever was shared with them directly
// stays, and they stay on the Album to see it; with nothing left of their
// own, the Album goes back to More albums and so do they.
export function LeaveAlbum({ album }: { album: ViewerAlbum }) {
  const leave = useLeaveAlbum(album.id);
  const navigate = useNavigate();
  return (
    <div className="mt-8">
      <ConfirmAction
        description={
          album.has_own_media
            ? "Photos and videos shared with you directly stay. The rest goes back to More albums."
            : "It goes back to More albums, where you can join it again."
        }
        error={leave.error}
        label="Leave album"
        onConfirm={() =>
          leave.mutateAsync().then(({ kept }) => {
            if (!kept) void navigate("/albums", { replace: true });
          })
        }
        pending={leave.isPending}
      />
    </div>
  );
}

// The line at the end of the viewer's own Album when more of it is offered
// to them, naming what kind of media. The link opens the preview on a tab
// that has more to see.
export function MoreAvailable({
  album,
  tab,
}: {
  album: ViewerAlbum;
  tab: ViewerTab;
}) {
  const photos = album.more_photo_count > 0;
  const videos = album.more_video_count > 0;
  if (!photos && !videos) return null;
  const target = !photos ? "videos" : !videos ? "photos" : tab;
  return (
    <p className="mt-14 border-t border-border pt-8 text-sm text-muted">
      More{" "}
      {photos && videos
        ? "photos and videos are"
        : photos
          ? "photos are"
          : "videos are"}{" "}
      available in this album.{" "}
      <Link
        className="-mx-2 rounded-sm px-2 py-1 text-foreground underline underline-offset-4 hover:bg-surface"
        to={`/albums/${encodeURIComponent(album.id)}/preview/${target}`}
      >
        See the full album
      </Link>
    </p>
  );
}
