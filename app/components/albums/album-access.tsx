import { useEffect, useId, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";

import {
  usePreviewAlbumAccess,
  usePreviewRemoveAccess,
  useRemoveAllAccess,
  useSaveAlbumAccess,
} from "../../hooks/queries/albums";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { cn } from "../../lib/utils";
import type {
  AccessPerson,
  AlbumCircle,
  AlbumDetail,
  AudienceChange,
} from "../../types/generated/publishing";
import { FieldError, Form, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { audienceSummary } from "./access-labels";
import { countLabel } from "./moment-labels";
import { PersonAvatar } from "./person-avatar";

// Who gains or loses media if the pending change is saved, shown before each
// explicit action. While the review loads it says so; with no review yet,
// placeholder explains how to get one, where the form has a way to. With
// neither, such as after a failed review, the section stays out of the way
// of the caller's error.
export function VisibilityReview({
  changes,
  album,
  className,
  pending = false,
  placeholder,
}: {
  changes: AudienceChange[] | null | undefined;
  album: AlbumDetail;
  className?: string;
  pending?: boolean;
  placeholder?: string;
}) {
  if (!changes && !pending && !placeholder) return null;
  return (
    <section
      aria-label="Visibility review"
      className={cn("border-y border-border py-5", className)}
    >
      <h3 className="font-heading text-xl">Visibility after saving</h3>
      {!changes ? (
        pending ? (
          <p className="mt-3 text-sm text-muted" role="status">
            Reviewing visibility…
          </p>
        ) : (
          <p className="mt-3 text-sm text-muted">{placeholder}</p>
        )
      ) : changes.length === 0 ? (
        <p className="mt-3 text-sm text-muted">No one gains or loses media.</p>
      ) : (
        <div className="mt-3 space-y-2">
          {changes.map((change) => (
            <p className="py-1 text-sm" key={change.person_id}>
              <strong>{change.display_name}</strong>{" "}
              <span className="text-muted">{audienceSummary(change)}</span>
            </p>
          ))}
        </div>
      )}
      {!album.published && (
        <p className="mt-3 text-xs text-muted">
          This album is unpublished. These changes apply to the view after
          publication.
        </p>
      )}
    </section>
  );
}

// Album-wide allows and Circle Offers with an explicit Save. Each change
// previews who gains or loses media before anything is written. People are
// grouped by how much of the Album recognized them, since someone in every
// Moment usually belongs here while someone in a few Moments is better served
// by Moment access.
export function AlbumAccess({ album }: { album: AlbumDetail }) {
  const [draft, setDraft] = useState<Record<string, boolean>>({});
  const [offers, setOffers] = useState<Record<string, boolean>>({});
  const review = usePreviewAlbumAccess(album.id);
  const save = useSaveAlbumAccess(album.id);
  const [removing, setRemoving] = useState<string | null>(null);
  const allowed = (id: string) =>
    draft[id] ??
    album.access.find((person) => person.person_id === id)?.decision ===
      "allow";
  const active = album.access.filter((person) => !person.deactivated);
  const frozen = album.access.filter((person) => person.deactivated);
  const changed = active.filter(
    (person) => (person.decision === "allow") !== allowed(person.person_id),
  );
  const offered = (circle: AlbumCircle) =>
    offers[circle.circle_id] ?? circle.offered;
  const changedCircles = album.circles.filter(
    (circle) => circle.offered !== offered(circle),
  );
  const dirty =
    save.isPending || changed.length > 0 || changedCircles.length > 0;
  useUnsavedChanges(dirty, true);
  const saveErrors = fieldErrors(save.error);
  const saveError =
    saveErrors.people ??
    saveErrors.person_id ??
    saveErrors.circles ??
    saveErrors.circle_id;
  const errorId = useId();
  const totalMoments = album.moments.length;
  const everywhere = active.filter(
    (person) => totalMoments > 0 && person.moments_detected === totalMoments,
  );
  const somewhere = active.filter(
    (person) =>
      person.moments_detected > 0 && person.moments_detected < totalMoments,
  );
  // People who joined come first among those not seen, so the Curator finds
  // them without scanning the whole list.
  const nowhere = active
    .filter((person) => person.moments_detected === 0)
    .sort((a, b) => Number(b.joined) - Number(a.joined));
  function change(
    next: Record<string, boolean>,
    nextOffers: Record<string, boolean> = offers,
  ) {
    setDraft(next);
    setOffers(nextOffers);
    save.reset();
    const body = payload(next, nextOffers);
    if (body.people.length + body.circles.length > 0) review.mutate(body);
    else review.reset();
  }
  const payload = (
    next: Record<string, boolean>,
    nextOffers: Record<string, boolean>,
  ) => ({
    people: active
      .filter(
        (person) =>
          person.person_id in next &&
          next[person.person_id] !== (person.decision === "allow"),
      )
      .map((person) => ({
        person_id: person.person_id,
        allowed: next[person.person_id],
      })),
    circles: album.circles
      .filter(
        (circle) =>
          circle.circle_id in nextOffers &&
          nextOffers[circle.circle_id] !== circle.offered,
      )
      .map((circle) => ({
        circle_id: circle.circle_id,
        offered: nextOffers[circle.circle_id],
      })),
  });
  const total = album.moments.reduce(
    (count, moment) => count + moment.entries.length,
    0,
  );
  const removingPerson = album.access.find(
    (person) => person.person_id === removing,
  );
  const personRow = (person: AccessPerson) => (
    <div
      className="flex items-center gap-3 border-t border-border py-3"
      key={person.person_id}
    >
      <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-sm">
        <input
          aria-label={`Album access for ${person.display_name}${person.joined ? ", joined" : ""}`}
          checked={allowed(person.person_id)}
          className="size-4 cursor-pointer accent-primary"
          onChange={(event) =>
            change({ ...draft, [person.person_id]: event.target.checked })
          }
          type="checkbox"
        />
        <PersonAvatar person={person} />
        <span className="min-w-0">
          <span className="flex min-w-0 items-center gap-2">
            <strong className="truncate font-medium">
              {person.display_name}
            </strong>
            {person.joined && (
              <span className="shrink-0 rounded-full bg-accent px-2 text-xs/5 font-medium text-muted">
                Joined
              </span>
            )}
          </span>
          <small className="block text-xs text-muted">
            {person.moments_detected > 0
              ? `Seen in ${person.moments_detected} of ${countLabel(totalMoments, "Moment", "Moments")}`
              : "Not seen in this album"}
          </small>
          <small className="block text-xs text-muted">
            {person.accessible_count} of {total} items accessible now
            {person.exceptions > 0 &&
              `, ${countLabel(person.exceptions, "exception", "exceptions")}`}
          </small>
        </span>
      </label>
      <Button
        className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground"
        onClick={() => setRemoving(person.person_id)}
        type="button"
        variant="ghost"
      >
        Remove all access…
      </Button>
    </div>
  );
  return (
    <section aria-labelledby="album-access-heading" className="max-w-180">
      <h2 className={sectionHeadingClass} id="album-access-heading">
        Album access
      </h2>
      <p className="mt-2 text-xs text-muted">
        Give someone access across the album. Moment and item exceptions still
        apply.
      </p>
      <Form
        aria-busy={save.isPending}
        aria-label="Album access"
        className="mt-6"
        error={save.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (save.isPending || !dirty) return;
          save.mutate(payload(draft, offers), {
            onSuccess: () => {
              setDraft({});
              setOffers({});
              review.reset();
            },
          });
        }}
      >
        <fieldset
          aria-describedby={saveError ? errorId : undefined}
          aria-invalid={!!saveError}
          className="min-w-0 space-y-6"
          disabled={save.isPending}
        >
          {active.length > 0 && (
            <>
              <PeopleGroup
                action={
                  everywhere.some((person) => !allowed(person.person_id)) && (
                    <Button
                      className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs"
                      onClick={() =>
                        change({
                          ...draft,
                          ...Object.fromEntries(
                            everywhere.map((person) => [
                              person.person_id,
                              true,
                            ]),
                          ),
                        })
                      }
                      type="button"
                      variant="ghost"
                    >
                      Allow everyone in every Moment
                    </Button>
                  )
                }
                empty="No one was recognized in every Moment."
                hint="Recognized in every Moment. Album access is usually right for them."
                id="access-everywhere"
                people={everywhere}
                title="In every Moment"
              >
                {personRow}
              </PeopleGroup>
              <PeopleGroup
                empty="No one was recognized in only part of the Album."
                hint="Recognized in part of the Album. Moment access is usually enough."
                id="access-somewhere"
                people={somewhere}
                title="In some Moments"
              >
                {personRow}
              </PeopleGroup>
              {nowhere.length > 0 && (
                <details>
                  <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
                    {countLabel(nowhere.length, "person", "people")} not seen in
                    this album
                  </summary>
                  {nowhere.map(personRow)}
                </details>
              )}
            </>
          )}
          {active.length === 0 && (
            <p className="border-t border-border py-3 text-sm text-muted">
              No active viewers to grant access to.
            </p>
          )}
          <section aria-labelledby="access-circles">
            <h3 className="text-sm font-medium" id="access-circles">
              Circles{" "}
              <span className="ml-1 text-xs font-normal text-muted">
                {album.circles.length}
              </span>
            </h3>
            <p className="mt-1 text-xs text-muted">
              Offer the album to a Circle. Its members can browse it under More
              albums. Access chosen for a person above always comes first.
            </p>
            <div className="mt-2">
              {album.circles.length > 0 ? (
                album.circles.map((circle) => (
                  <label
                    className="flex cursor-pointer items-center gap-3 border-t border-border py-3 text-sm"
                    key={circle.circle_id}
                  >
                    <input
                      aria-label={`Offer to ${circle.name}`}
                      checked={offered(circle)}
                      className="size-4 cursor-pointer accent-primary"
                      onChange={(event) =>
                        change(draft, {
                          ...offers,
                          [circle.circle_id]: event.target.checked,
                        })
                      }
                      type="checkbox"
                    />
                    <span className="min-w-0">
                      <strong className="block truncate font-medium">
                        {circle.name}
                      </strong>
                      <small className="block text-xs text-muted">
                        {countLabel(circle.member_count, "person", "people")}
                      </small>
                    </span>
                  </label>
                ))
              ) : (
                <p className="border-t border-border py-3 text-xs text-muted">
                  No Circles yet.{" "}
                  <Link
                    className="text-accent-foreground underline-offset-4 hover:underline"
                    to="/curator/circles"
                  >
                    Create one
                  </Link>{" "}
                  to offer this album to a group of people.
                </p>
              )}
            </div>
          </section>
          {frozen.length > 0 && (
            <details>
              <summary className="-mx-2 cursor-pointer rounded-sm px-2 py-2 text-xs text-accent-foreground hover:bg-surface">
                {countLabel(
                  frozen.length,
                  "deactivated person",
                  "deactivated people",
                )}{" "}
                still {frozen.length === 1 ? "has" : "have"} rules here
              </summary>
              <p className="mt-1 text-xs text-muted">
                Deactivation stops their access without deleting these rules,
                which apply again if they are reactivated.
              </p>
              {frozen.map((person) => (
                <div
                  className="mt-2 flex items-center gap-3 border-t border-border py-3"
                  key={person.person_id}
                >
                  <PersonAvatar person={person} />
                  <span className="min-w-0 flex-1 text-sm">
                    <strong className="block truncate font-medium">
                      {person.display_name}
                    </strong>
                    <small className="block text-xs text-muted">
                      Deactivated.{" "}
                      {countLabel(
                        person.exceptions +
                          (person.decision === "allow" ? 1 : 0),
                        "rule",
                        "rules",
                      )}{" "}
                      would allow {person.accessible_count} of {total} items.
                    </small>
                  </span>
                  <Button
                    className="-mx-2 h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground"
                    onClick={() => setRemoving(person.person_id)}
                    type="button"
                    variant="ghost"
                  >
                    Remove all access…
                  </Button>
                </div>
              ))}
            </details>
          )}
          <FieldError error={saveError} id={errorId} />
          <p className="mt-3 text-xs text-muted">
            Unchecking a person removes only Album-wide access. Their Moment and
            item decisions stay in place.
          </p>
          <VisibilityReview
            album={album}
            changes={dirty ? review.data?.changes : undefined}
            className="my-6"
            pending={dirty && review.isPending}
            placeholder={
              review.isError ? undefined : "Change access above to review it."
            }
          />
          {review.isError && (
            <p className="mb-4 text-xs text-destructive" role="alert">
              Could not review visibility. You can still save.
            </p>
          )}
          <Button disabled={!dirty} type="submit">
            {save.isPending ? "Saving…" : "Save Album access"}
          </Button>
          {save.isSuccess && !dirty && (
            <p className="mt-4 text-sm text-muted" role="status">
              Album access saved.
            </p>
          )}
        </fieldset>
      </Form>
      {removingPerson && (
        <RemoveAllAccessDialog
          album={album}
          onClose={() => setRemoving(null)}
          person={removingPerson}
        />
      )}
    </section>
  );
}

function PeopleGroup({
  id,
  title,
  hint,
  empty,
  action,
  people,
  children,
}: {
  id: string;
  title: string;
  hint: string;
  empty: string;
  action?: ReactNode;
  people: AccessPerson[];
  children: (person: AccessPerson) => ReactNode;
}) {
  return (
    <section aria-labelledby={id}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-medium" id={id}>
          {title}{" "}
          <span className="ml-1 text-xs font-normal text-muted">
            {people.length}
          </span>
        </h3>
        {action}
      </div>
      <p className="mt-1 text-xs text-muted">{hint}</p>
      <div className="mt-2">
        {people.length > 0 ? (
          people.map(children)
        ) : (
          <p className="border-t border-border py-3 text-xs text-muted">
            {empty}
          </p>
        )}
      </div>
    </section>
  );
}

function RemoveAllAccessDialog({
  album,
  person,
  onClose,
}: {
  album: AlbumDetail;
  person: { person_id: string; display_name: string };
  onClose: () => void;
}) {
  const review = usePreviewRemoveAccess(album.id);
  const remove = useRemoveAllAccess(album.id);
  const reviewPerson = review.mutate;
  useEffect(() => {
    reviewPerson({ person_id: person.person_id });
  }, [reviewPerson, person.person_id]);
  const returnFocus = useReturnFocus();
  return (
    <Dialog
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
      open
    >
      <DialogContent onCloseAutoFocus={returnFocus}>
        <DialogTitle className="pr-8">
          Remove all access for {person.display_name}?
        </DialogTitle>
        <DialogDescription className="mt-3 text-sm text-muted">
          Every Album, Moment, and item decision for {person.display_name} in
          this album is removed. Other people's access stays unchanged.
        </DialogDescription>
        <VisibilityReview
          album={album}
          changes={review.data?.changes}
          className="my-6"
          pending={review.isPending || review.isIdle}
        />
        {review.isError && (
          <p className="mb-4 text-sm text-destructive" role="alert">
            Could not review this person's access. Close and try again.
          </p>
        )}
        <Form
          aria-busy={remove.isPending}
          aria-label={`Remove all access for ${person.display_name}`}
          error={remove.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!review.data || remove.isPending) return;
            remove.mutate(
              {
                person_id: person.person_id,
                review_token: review.data.review_token,
              },
              { onSuccess: onClose },
            );
          }}
        >
          <fieldset className="flex gap-2" disabled={remove.isPending}>
            <Button disabled={!review.data} type="submit" variant="destructive">
              {remove.isPending ? "Removing…" : "Remove all access"}
            </Button>
            <Button onClick={onClose} type="button" variant="outline">
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
