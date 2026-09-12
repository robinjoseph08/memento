import { useLayoutEffect, useRef } from "react";

import {
  usePublicationReview,
  usePublishAlbum,
} from "../../hooks/queries/publication";
import { Form, ReadFailure } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { countLabel } from "./moment-labels";

export function PublicationReviewDialog({
  albumID,
  onClose,
}: {
  albumID: string;
  onClose: () => void;
}) {
  const review = usePublicationReview(albumID);
  const publish = usePublishAlbum(albumID);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, []);
  return (
    <Dialog
      onOpenChange={(open) => !open && !publish.isPending && onClose()}
      open
    >
      <DialogContent
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          if (returnFocusRef.current?.isConnected)
            returnFocusRef.current.focus();
        }}
      >
        <DialogTitle>Review & publish</DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Publishing makes this Album available to the people below. It does not
          send notifications.
        </DialogDescription>
        {review.isPending && (
          <p className="mt-5 text-sm" role="status">
            Reviewing publication…
          </p>
        )}
        {review.isError && (
          <ReadFailure
            error={review.error}
            pending={review.isFetching}
            retry={review.refetch}
          />
        )}
        {review.data && (
          <Form
            aria-busy={publish.isPending}
            aria-label="Publish Album"
            className="mt-5"
            error={publish.error}
            onSubmit={(event) => {
              event.preventDefault();
              if (
                !publish.isPending &&
                !review.isFetching &&
                !review.isError &&
                review.data.blockers.length === 0
              )
                publish.mutate(
                  { review_token: review.data.review_token },
                  { onSuccess: onClose },
                );
            }}
          >
            <p className="text-sm">{review.data.title}</p>
            <p className="mt-1 text-xs text-muted">
              {countLabel(review.data.photo_count, "photo", "photos")},{" "}
              {countLabel(review.data.video_count, "video", "videos")}
            </p>
            <ul className="my-5">
              {review.data.audience.map((person) => (
                <li
                  className="flex justify-between gap-4 border-t border-border py-3 text-sm"
                  key={person.person_id}
                >
                  <span>{person.display_name}</span>
                  <span className="text-muted">
                    {countLabel(person.photo_count, "photo", "photos")},{" "}
                    {countLabel(person.video_count, "video", "videos")}
                  </span>
                </li>
              ))}
            </ul>
            {review.data.audience.length === 0 && (
              <p className="mb-4 text-sm text-muted">
                No viewers have access. This does not block publication.
              </p>
            )}
            {review.data.blockers.map((message) => (
              <p className="my-2 text-sm text-destructive" key={message}>
                {message}
              </p>
            ))}
            {review.data.warnings.map((message) => (
              <p className="my-2 text-sm text-muted" key={message}>
                {message}
              </p>
            ))}
            <div className="mt-5 flex gap-2">
              <Button
                disabled={
                  publish.isPending ||
                  review.isFetching ||
                  review.isError ||
                  review.data.blockers.length > 0
                }
                type="submit"
              >
                {publish.isPending ? "Publishing…" : "Publish Album"}
              </Button>
              <Button
                disabled={publish.isPending}
                onClick={onClose}
                type="button"
                variant="outline"
              >
                Keep editing
              </Button>
            </div>
          </Form>
        )}
      </DialogContent>
    </Dialog>
  );
}
