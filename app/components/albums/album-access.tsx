import { useState } from "react";

import {
  usePreviewRemoveAccess,
  useRemoveAllAccess,
} from "../../hooks/queries/albums";
import type { AlbumDetail } from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { Failure, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";
import { countLabel } from "./moment-labels";
import { ScopeAccess } from "./scope-access";

export function AlbumAccess({ album }: { album: AlbumDetail }) {
  const preview = usePreviewRemoveAccess(album.id);
  const remove = useRemoveAllAccess(album.id);
  const [reviewOpen, setReviewOpen] = useState(false);
  const review = preview.data;
  return (
    <section aria-labelledby="album-access-heading" className="max-w-180">
      <h2 className={sectionHeadingClass} id="album-access-heading">
        Album access
      </h2>
      <p className="mt-2 text-sm text-muted">
        Give someone access across the Album. Moment and item exceptions still
        apply.
      </p>
      <ScopeAccess
        albumID={album.id}
        people={album.access}
        personAction={(person) => (
          <Button
            aria-label={`Remove all access for ${person.display_name}`}
            disabled={preview.isPending}
            onClick={() => {
              remove.reset();
              preview.mutate(
                { person_id: person.person_id },
                { onSuccess: () => setReviewOpen(true) },
              );
            }}
            size="sm"
            type="button"
            variant="ghost"
          >
            Remove all access…
          </Button>
        )}
        total={album.moments.reduce(
          (count, moment) => count + moment.entries.length,
          0,
        )}
      />
      <Failure error={preview.error} />
      {preview.isPending && (
        <p className="mt-3 text-sm text-muted" role="status">
          Reviewing access…
        </p>
      )}
      {review && (
        <ConfirmDialog
          confirmLabel="Remove all access"
          description={`${countLabel(review.album_decisions, "Album decision", "Album decisions")}, ${countLabel(review.moment_decisions, "Moment decision", "Moment decisions")}, ${countLabel(review.entry_decisions, "item decision", "item decisions")}. ${review.display_name} currently has access to ${countLabel(review.accessible_count, "item", "items")}. Removing all decisions leaves no access in this Album.`}
          error={remove.error}
          onConfirm={() =>
            remove.mutate(
              {
                person_id: review.person_id,
                review_token: review.review_token,
              },
              { onSuccess: () => setReviewOpen(false) },
            )
          }
          onOpenChange={setReviewOpen}
          open={reviewOpen}
          pending={remove.isPending}
          title={`Remove all access for ${review.display_name}?`}
        />
      )}
      <p className="mt-5 text-xs text-muted">
        Unchecking removes only Album-wide access. Moment and item decisions
        stay in place.
      </p>
    </section>
  );
}
