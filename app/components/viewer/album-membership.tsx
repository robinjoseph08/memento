import { Link, useNavigate } from "react-router-dom";

import {
  useJoinAlbum,
  useLeaveAlbum,
  type ViewerTab,
} from "../../hooks/queries/viewer";
import { errorMessage } from "../../lib/http";
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
// stays; the rest goes back to More albums.
export function LeaveAlbum({ albumID }: { albumID: string }) {
  const leave = useLeaveAlbum(albumID);
  const navigate = useNavigate();
  return (
    <div className="mt-8">
      <ConfirmAction
        description="Photos and videos shared with you directly stay. You can join it again from More albums."
        error={leave.error}
        label="Leave album"
        onConfirm={() =>
          leave
            .mutateAsync()
            .then(() => void navigate("/albums", { replace: true }))
        }
        pending={leave.isPending}
      />
    </div>
  );
}

// The line at the end of the viewer's own Album when more of it is offered
// to them.
export function MoreAvailable({
  albumID,
  tab,
}: {
  albumID: string;
  tab: ViewerTab;
}) {
  return (
    <p className="mt-14 border-t border-border pt-8 text-sm text-muted">
      More of this album is available to you.{" "}
      <Link
        className="-mx-2 rounded-sm px-2 py-1 text-foreground underline underline-offset-4 hover:bg-surface"
        to={`/albums/${encodeURIComponent(albumID)}/preview/${tab}`}
      >
        See the full album
      </Link>
    </p>
  );
}
