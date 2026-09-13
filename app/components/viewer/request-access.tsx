import { useRequestAlbumAccess } from "../../hooks/queries/admission";
import { errorMessage } from "../../lib/http";
import { Button } from "../ui/button";

// Visiting an inaccessible Album creates nothing. Only this explicit action
// records a request for the Curator, and repeating it reuses the same request.
export function RequestAccess({ albumID }: { albumID: string }) {
  const request = useRequestAlbumAccess(albumID);
  if (request.isSuccess)
    return (
      <p className="mt-4 text-muted" role="status">
        {request.data.status === "denied"
          ? "A Curator has already reviewed a request for this album."
          : "Your Curator has been asked to share this album with you."}
      </p>
    );
  return (
    <div className="mt-4">
      <Button
        disabled={request.isPending}
        onClick={() => request.mutate()}
        variant="outline"
      >
        {request.isPending ? "Sending request…" : "Request access"}
      </Button>
      {request.isError && (
        <p className="mt-3 text-sm text-destructive" role="alert">
          {errorMessage(request.error)}
        </p>
      )}
    </div>
  );
}
