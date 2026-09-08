import { useRetryAlbum } from "../../hooks/queries/albums";
import type { AlbumDetail } from "../../types/generated/publishing";
import { Form, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";

export function ImportProgress({ album }: { album: AlbumDetail }) {
  const retry = useRetryAlbum(album.id);
  const labels: Record<string, string> = {
    queued: "Waiting to import",
    processing: "Importing your album",
    interrupted: "Import interrupted",
    failed: "Import failed",
  };
  const active = album.status === "queued" || album.status === "processing";
  return (
    <section className="max-w-160 py-8">
      <h2 className={sectionHeadingClass}>
        {labels[album.status] ?? "Import status"}
      </h2>
      <div className="mt-4" role="status">
        <p>
          {album.message ||
            (album.status === "queued"
              ? "Your album is queued. You can leave this page and return later."
              : album.status === "processing"
                ? "Preparing your photos and videos. You can leave this page and return later."
                : "The import did not finish. Try importing this album again.")}
        </p>
        {active && (
          <>
            <p className="mt-3 text-sm text-muted">
              {album.processed} of {album.total} items processed
            </p>
            {album.total > 0 && (
              <progress
                aria-label="Import progress"
                className="mt-3 h-2 w-full accent-primary"
                max={album.total}
                value={album.processed}
              />
            )}
          </>
        )}
      </div>
      {(album.status === "failed" || album.status === "interrupted") && (
        <Form
          aria-busy={retry.isPending}
          aria-label="Retry import"
          className="mt-5"
          error={retry.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!retry.isPending) retry.mutate();
          }}
        >
          <Button disabled={retry.isPending} type="submit">
            {retry.isPending ? "Retrying…" : "Retry import"}
          </Button>
        </Form>
      )}
    </section>
  );
}
