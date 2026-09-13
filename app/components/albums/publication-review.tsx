import {
  usePublicationReview,
  usePublishAlbum,
} from "../../hooks/queries/publication";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { cn } from "../../lib/utils";
import type { AlbumDetail } from "../../types/generated/publishing";
import { Form, ReadFailure } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { countLabel } from "./moment-labels";
import { PersonAvatar } from "./person-avatar";

// Review & publish is the only command bar action and exists only while the
// Album is unpublished. It confirms the audience and blockers before publishing.
export function PublishDialog({
  album,
  onClose,
}: {
  album: AlbumDetail;
  onClose: () => void;
}) {
  const review = usePublicationReview(album.id);
  const publish = usePublishAlbum(album.id);
  const pending = publish.isPending;
  const returnFocus = useReturnFocus();
  const data = review.data;
  const total = data ? data.photo_count + data.video_count : 0;
  const blocked = !data || data.blockers.length > 0;
  return (
    <Dialog onOpenChange={(open) => !open && !pending && onClose()} open>
      <DialogContent className="max-w-xl" onCloseAutoFocus={returnFocus}>
        <DialogTitle className="pr-8">Ready to publish?</DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Publishing makes this album available to the people below. It sends no
          notifications.
        </DialogDescription>
        {review.isPending && (
          <p className="mt-5 text-sm" role="status">
            Reviewing the audience…
          </p>
        )}
        {review.isError && (
          <ReadFailure
            error={review.error}
            pending={review.isFetching}
            retry={review.refetch}
          />
        )}
        {data && (
          <>
            <section
              aria-label="Audience"
              className="mt-5 border-t border-border"
            >
              {data.audience.length ? (
                data.audience.map((person) => (
                  <div
                    className="flex items-center gap-3 border-b border-border py-3 text-sm"
                    key={person.person_id}
                  >
                    <PersonAvatar person={person} />
                    <strong className="font-medium">
                      {person.display_name}
                    </strong>
                    <span className="ml-auto text-xs text-muted">
                      {person.accessible_count} of {total} items
                    </span>
                  </div>
                ))
              ) : (
                <p className="border-b border-border py-3 text-sm text-muted">
                  No one has access yet. You can publish now and grant access
                  later.
                </p>
              )}
            </section>
            <ul className="mt-5 space-y-2 text-sm">
              <li>
                {countLabel(total, "item", "items")} in{" "}
                {countLabel(data.moment_count, "Moment", "Moments")}
              </li>
              {data.warnings.map((warning) => (
                <li className="text-muted" key={warning}>
                  {warning}
                </li>
              ))}
            </ul>
            {data.blockers.map((blocker) => (
              <p
                className="mt-4 text-sm text-destructive"
                key={blocker}
                role="alert"
              >
                {blocker}
              </p>
            ))}
          </>
        )}
        <Form
          aria-busy={pending}
          aria-label="Publish album"
          className={cn("mt-6", !data && "hidden")}
          error={publish.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (pending || !data || review.isFetching || blocked) return;
            publish.mutate(
              { review_token: data.review_token },
              { onSuccess: onClose, onError: () => void review.refetch() },
            );
          }}
        >
          <fieldset className="flex flex-wrap gap-2" disabled={pending}>
            <Button disabled={blocked || review.isFetching} type="submit">
              {pending ? "Publishing…" : "Publish album"}
            </Button>
            <Button onClick={onClose} type="button" variant="outline">
              Keep editing
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}

export function PublishChecklist({ album }: { album: AlbumDetail }) {
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  const checks = [
    { ok: !!album.title.trim(), label: "Album has a title" },
    {
      ok: total > 0,
      label: `${countLabel(total, "item", "items")} assigned to ${countLabel(album.moments.length, "Moment", "Moments")}`,
    },
  ];
  return (
    <section
      aria-labelledby="before-publish"
      className="mt-9 border-t border-border pt-6"
    >
      <h3 className="font-heading text-xl" id="before-publish">
        Before you publish
      </h3>
      <ul className="mt-4 space-y-2 text-sm">
        {checks.map((check) => (
          <li className="flex items-center gap-3" key={check.label}>
            <span
              aria-hidden="true"
              className={cn(
                "inline-block size-2 rounded-full",
                check.ok ? "bg-primary" : "bg-destructive",
              )}
            />
            {check.label}
          </li>
        ))}
      </ul>
      <p className="mt-4 text-xs leading-relaxed text-muted">
        Review Moment access and preview as a person, then publish when you're
        ready. Suggestions and unlinked faces never block publication.
      </p>
    </section>
  );
}
