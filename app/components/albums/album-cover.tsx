import { useId, useState, type ReactNode } from "react";

import {
  useSaveCoverOrder,
  useViewingGroups,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  AlbumDetail,
  Moment,
  ViewingGroup,
  ViewingPerson,
} from "../../types/generated/publishing";
import {
  FieldError,
  Form,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { Button } from "../ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";
import { AlbumImage } from "./album-image";
import { Audience } from "./audience";
import { countLabel, momentCover, momentHeading } from "./moment-labels";

// Moment labels listed before the rest collapse into a count.
const shownMoments = 6;

// The Cover Order as saved: preferred Moments by position.
function savedOrder(album: AlbumDetail) {
  return album.moments
    .filter((moment) => moment.cover_position > 0)
    .sort((a, b) => a.cover_position - b.cover_position)
    .map((moment) => moment.id);
}

function sameOrder(a: string[], b: string[]) {
  return a.length === b.length && a.every((id, index) => id === b[index]);
}

// The cover a Viewing Group gets: the first preferred Moment its members can
// see, otherwise the earliest they can see. Mirrors the server rule so the
// pane can preview an unsaved order.
function groupCover(group: ViewingGroup, order: string[]) {
  return (
    order.find((id) => group.moment_ids.includes(id)) ?? group.moment_ids[0]
  );
}

// The Curator's Cover Order for one Album, with a live preview of which
// cover each Viewing Group will see. Reordering is a draft until Save, like
// the access panes.
export function AlbumCover({ album }: { album: AlbumDetail }) {
  const groups = useViewingGroups(album.id);
  const save = useSaveCoverOrder(album.id);
  const [draft, setDraft] = useState<string[] | null>(null);
  const saved = savedOrder(album);
  const byID = new Map(album.moments.map((moment) => [moment.id, moment]));
  // A Moment can vanish under a draft, through a sync or another tab, so
  // the working order only ever holds Moments the Album still has.
  const order = (draft ?? saved).filter((id) => byID.has(id));
  const dirty = save.isPending || (draft !== null && !sameOrder(order, saved));
  useUnsavedChanges(dirty, true);
  const errorId = useId();
  const preferred = order.flatMap((id) => byID.get(id) ?? []);
  const rest = album.moments.filter((moment) => !order.includes(moment.id));
  function change(next: string[]) {
    setDraft(next);
    save.reset();
  }
  function move(id: string, offset: number) {
    const index = order.indexOf(id);
    const target = index + offset;
    if (index < 0 || target < 0 || target >= order.length) return;
    const next = [...order];
    next.splice(index, 1);
    next.splice(target, 0, id);
    change(next);
  }
  return (
    <section aria-labelledby="album-cover-heading" className="max-w-180">
      <h2 className={sectionHeadingClass} id="album-cover-heading">
        Album cover
      </h2>
      <p className="mt-2 text-xs text-muted">
        Each viewer sees the cover of the first preferred Moment they can
        access, then the earliest Moment they can access.
      </p>
      {album.moments.length === 0 ? (
        <p className="mt-6 text-sm text-muted">
          This Album had no photos or videos to import.
        </p>
      ) : album.moments.length === 1 ? (
        <p className="mt-6 text-sm text-muted">
          This Album has one Moment, so its cover is the Album cover. Change it
          from the Moment with Set as cover.
        </p>
      ) : (
        <Form
          aria-busy={save.isPending}
          aria-label="Album cover"
          className="mt-6"
          error={save.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (save.isPending || !dirty) return;
            save.mutate(
              { moment_ids: order },
              { onSuccess: () => setDraft(null) },
            );
          }}
        >
          <fieldset
            aria-describedby={fieldErrors(save.error).moment_ids && errorId}
            className="min-w-0"
            disabled={save.isPending}
          >
            <CoverRow
              empty="No preferred Moments yet. Prefer one below to show its cover first."
              id="preferred-covers-heading"
              moments={preferred}
              title="Preferred covers"
            >
              {(moment, index) => (
                <CoverTile key={moment.id} moment={moment} position={index + 1}>
                  <TileAction
                    disabled={index === 0}
                    label={`Move ${momentHeading(moment).title} earlier`}
                    onClick={() => move(moment.id, -1)}
                  >
                    Earlier
                  </TileAction>
                  <TileAction
                    disabled={index === preferred.length - 1}
                    label={`Move ${momentHeading(moment).title} later`}
                    onClick={() => move(moment.id, 1)}
                  >
                    Later
                  </TileAction>
                  <TileAction
                    label={`Remove ${momentHeading(moment).title} from preferred covers`}
                    onClick={() =>
                      change(order.filter((id) => id !== moment.id))
                    }
                  >
                    Remove
                  </TileAction>
                </CoverTile>
              )}
            </CoverRow>
            <CoverRow
              empty="Every Moment is preferred."
              id="capture-order-heading"
              moments={rest}
              title="Then in capture order"
            >
              {(moment) => (
                <CoverTile key={moment.id} moment={moment}>
                  <TileAction
                    label={`Prefer ${momentHeading(moment).title}`}
                    onClick={() => change([...order, moment.id])}
                  >
                    Prefer
                  </TileAction>
                </CoverTile>
              )}
            </CoverRow>
            <FieldError
              error={fieldErrors(save.error).moment_ids}
              id={errorId}
            />
          </fieldset>
          <div className="mt-6 flex flex-wrap items-center gap-4">
            <Button disabled={!dirty} type="submit">
              {save.isPending ? "Saving…" : "Save cover order"}
            </Button>
            {save.isSuccess && !dirty && (
              <p className="text-sm text-muted" role="status">
                Cover order saved.
              </p>
            )}
          </div>
        </Form>
      )}
      {album.moments.length > 0 && (
        <section
          aria-labelledby="viewing-groups-heading"
          className="mt-8 border-t border-border pt-6"
        >
          <h3 className="font-heading text-xl" id="viewing-groups-heading">
            Who sees which cover
          </h3>
          <p className="mt-2 text-xs text-muted">
            People who can see the same Moment covers are grouped together.
            {!album.published &&
              " This Album is unpublished, so this is the view after publication."}
          </p>
          {groups.isPending && (
            <p className="mt-4 text-sm text-muted" role="status">
              Working out who sees what…
            </p>
          )}
          {groups.isError && (
            <div className="mt-4">
              <ReadFailure
                error={groups.error}
                pending={groups.isFetching}
                retry={groups.refetch}
              />
            </div>
          )}
          {groups.data &&
            (groups.data.groups.length === 0 &&
            groups.data.placeholder.length === 0 ? (
              <p className="mt-4 text-sm text-muted">
                No one has access yet. Add people under Album access.
              </p>
            ) : (
              <ul className="mt-2">
                {groups.data.groups.map((group) => {
                  const cover = byID.get(groupCover(group, order));
                  return (
                    <GroupRow
                      cover={cover}
                      key={group.moment_ids.join(",")}
                      moments={group.moment_ids.flatMap(
                        (id) => byID.get(id) ?? [],
                      )}
                      people={group.people}
                    />
                  );
                })}
                {groups.data.placeholder.length > 0 && (
                  <GroupRow moments={[]} people={groups.data.placeholder} />
                )}
              </ul>
            ))}
        </section>
      )}
    </section>
  );
}

// One zone of the Cover Order: its Moments as tiles, or the empty line.
function CoverRow({
  id,
  title,
  empty,
  moments,
  children: renderTile,
}: {
  id: string;
  title: string;
  empty: string;
  moments: Moment[];
  children: (moment: Moment, index: number) => ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="mt-5">
      <h3 className="text-sm font-medium" id={id}>
        {title}
      </h3>
      {moments.length === 0 ? (
        <p className="mt-2 text-xs text-muted">{empty}</p>
      ) : (
        <ul className="mt-3 flex flex-wrap gap-4">{moments.map(renderTile)}</ul>
      )}
    </section>
  );
}

// A Moment's cover as the Album list will crop it, with the Moment's label
// and the actions that move it through the Cover Order.
function CoverTile({
  moment,
  position,
  children,
}: {
  moment: Moment;
  position?: number;
  children: ReactNode;
}) {
  const cover = momentCover(moment);
  const heading = momentHeading(moment);
  return (
    <li
      aria-label={heading.title}
      className="flex w-36 flex-col gap-2 min-[400px]:w-40"
    >
      <span className="relative block">
        <AlbumImage
          alt=""
          className="aspect-square w-full object-cover"
          fallback="No cover"
          src={cover?.available ? cover.thumbnail_url : ""}
        />
        {position !== undefined && (
          <span className="absolute top-1.5 left-1.5 flex size-6 items-center justify-center rounded-full bg-background text-xs font-medium shadow-sm">
            {position}
          </span>
        )}
      </span>
      <span className="line-clamp-2 text-xs font-medium">{heading.title}</span>
      <span className="-mx-2 flex flex-wrap">{children}</span>
    </li>
  );
}

// Earlier and Later at the ends of the order stay focusable so a keyboard
// user is not dropped to the page when their own move disables the button.
function TileAction({
  label,
  disabled = false,
  onClick,
  children,
}: {
  label: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      aria-disabled={disabled || undefined}
      aria-label={label}
      className="h-auto min-h-0 px-2 py-1 text-xs text-accent-foreground aria-disabled:opacity-50"
      onClick={() => {
        if (!disabled) onClick();
      }}
      size="sm"
      type="button"
      variant="ghost"
    >
      {children}
    </Button>
  );
}

// One Viewing Group: the cover its members get, who they are, and which
// Moment covers they can see. Without a cover it is the placeholder row.
function GroupRow({
  cover,
  moments,
  people,
}: {
  cover?: Moment;
  moments: Moment[];
  people: ViewingPerson[];
}) {
  const coverEntry = cover && momentCover(cover);
  const labels = moments.map((moment) => momentHeading(moment).title);
  const shown = labels.slice(0, shownMoments);
  const overflow = labels.length - shown.length;
  return (
    <li className="flex items-start gap-4 border-t border-border py-4 first:border-t-0">
      <AlbumImage
        alt=""
        className="aspect-square w-16 shrink-0 object-cover"
        fallback="Placeholder"
        src={coverEntry?.available ? coverEntry.thumbnail_url : ""}
      />
      <div className="min-w-0 flex-1">
        <p className="text-sm">
          <strong className="font-medium">
            {countLabel(people.length, "person", "people")}
          </strong>{" "}
          <span className="text-muted">
            {people.length === 1 ? "sees " : "see "}
            {cover
              ? `the cover of ${momentHeading(cover).title}`
              : "a placeholder"}
          </span>
        </p>
        <span className="mt-1.5 block">
          <Audience people={people} />
        </span>
        {labels.length > 0 && (
          <p className="mt-1.5 text-xs text-muted">
            Can see {shown.join(", ")}
            {overflow > 0 && (
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="cursor-default underline decoration-dotted">
                      {" "}
                      and {countLabel(overflow, "more", "more")}
                    </span>
                  </TooltipTrigger>
                  <TooltipContent align="start" side="bottom">
                    <ul className="space-y-1">
                      {moments.slice(shownMoments).map((moment) => (
                        <li key={moment.id}>{momentHeading(moment).title}</li>
                      ))}
                    </ul>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )}
          </p>
        )}
      </div>
    </li>
  );
}
