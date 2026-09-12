// PROTOTYPE. The chosen Curator album editor, "Outline". Master-detail: a
// left outline lists the Album sections and every Moment with its audience,
// and the right pane shows one Moment at a time with its access strip above a
// full-width grid. On narrow screens the outline is the first screen and
// Moments drill down.
import { ChevronLeft } from "lucide-react";
import { useEffect } from "react";
import { Link } from "react-router-dom";

import { useRefreshMomentFaces } from "../../../hooks/queries/albums";
import { useMediaQuery } from "../../../hooks/use-media-query";
import { cn } from "../../../lib/utils";
import { TitleForm } from "../../albums/album-detail";
import { AlbumImage } from "../../albums/album-image";
import { EntryPreview } from "../../albums/entry-preview";
import { sectionHeadingClass } from "../../people/form-fields";
import { BackLink } from "../../shell/back-link";
import { Button } from "../../ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../../ui/tooltip";
import type { PrototypeAccessPerson } from "./model";
import {
  AccessInspector,
  AlbumAccessSection,
  countLabel,
  mediaCounts,
  momentCover,
  momentHeading,
  PersonAvatar,
  PublicationStatus,
  PublishChecklist,
  SelectionActions,
  useMomentByParam,
  useMomentDialogs,
  useSelection,
  useURLState,
  ViewerPreviewSection,
  type MomentDialogs,
  type PrototypeAlbum,
  type PrototypeMoment,
} from "./shared";

function shortDay(day: string) {
  return new Date(`${day}T00:00:00Z`).toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
  });
}

const albumSections = [
  { key: "details", label: "Album details" },
  { key: "access", label: "Album access" },
  { key: "preview", label: "Viewer preview" },
] as const;

export function AlbumEditor({ album }: { album: PrototypeAlbum }) {
  const { params, link } = useURLState();
  const dialogs = useMomentDialogs(album);
  const desktop = useMediaQuery("(min-width: 761px)");
  const section = albumSections.some(
    (item) => item.key === params.get("section"),
  )
    ? (params.get("section") as (typeof albumSections)[number]["key"])
    : "moments";
  const moment = useMomentByParam(album);
  const drilled = params.get("pane") === "detail";
  const showOutline = desktop || !drilled;
  const showDetail = desktop || drilled;
  const entries = album.moments.flatMap((item) => item.entries);
  const rowClass = (active: boolean) =>
    cn(
      "flex min-h-11 items-center gap-3 rounded-sm border-l-2 px-3 text-left hover:bg-surface",
      active
        ? "border-primary bg-surface text-foreground"
        : "border-transparent",
    );

  return (
    <>
      <header className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b border-border px-5 py-4 min-[761px]:px-8">
        <div className="min-w-0 flex-1">
          <BackLink className="mb-2 text-xs" to="/curator">
            All albums
          </BackLink>
          <h1 className="font-heading text-[clamp(24px,3vw,30px)] leading-tight tracking-[-0.5px] wrap-anywhere">
            {album.title}
          </h1>
          <p className="mt-1 text-xs text-muted">
            {mediaCounts(entries)}
            <span className="ml-3 border-l border-border pl-3">
              <PublicationStatus album={album} />
            </span>
          </p>
        </div>
        <Button className="w-full min-[761px]:w-auto" onClick={dialogs.publish}>
          {album.published ? "Published" : "Review & publish"}
        </Button>
      </header>
      <div className="min-[761px]:grid min-[761px]:min-h-[70vh] min-[761px]:grid-cols-[300px_minmax(0,1fr)]">
        {showOutline && (
          <nav
            aria-label="Album outline"
            className="px-3 py-5 min-[761px]:sticky min-[761px]:top-0 min-[761px]:max-h-dvh min-[761px]:overflow-y-auto min-[761px]:border-r min-[761px]:border-border"
          >
            <p className="px-3 text-xs text-muted">Album</p>
            <ul className="mt-1 space-y-0.5">
              {albumSections.map((item) => (
                <li key={item.key}>
                  <Link
                    aria-current={section === item.key ? "page" : undefined}
                    className={cn(rowClass(section === item.key), "text-sm")}
                    to={link({ section: item.key, pane: "detail" })}
                  >
                    {item.label}
                  </Link>
                </li>
              ))}
            </ul>
            <p className="mt-6 px-3 text-xs text-muted">
              Moments{" "}
              <span className="ml-1 text-accent-foreground">
                {album.moments.length}
              </span>
            </p>
            <ul className="mt-1 space-y-0.5">
              {album.moments.map((item) => {
                const heading = momentHeading(item);
                const cover = momentCover(item);
                const active = section === "moments" && item.id === moment?.id;
                const suggestions = item.access.people.filter(
                  (person) => person.suggested,
                ).length;
                return (
                  <li key={item.id}>
                    <Link
                      aria-current={active ? "page" : undefined}
                      className={cn(rowClass(active), "items-start py-2.5")}
                      to={link({
                        section: "moments",
                        moment: item.id,
                        pane: "detail",
                        media: null,
                      })}
                    >
                      <AlbumImage
                        alt=""
                        className="mt-0.5 h-auto max-h-10 w-auto max-w-14 shrink-0"
                        fallback="No cover"
                        src={cover?.available ? cover.thumbnail_url : ""}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="line-clamp-2 text-sm font-medium">
                          {heading.title}
                        </span>
                        <span className="block text-xs text-muted">
                          {item.date && <>{shortDay(item.date)}, </>}
                          {mediaCounts(item.entries)}
                        </span>
                        <OutlineAudience
                          people={item.access.people}
                          suggestions={suggestions}
                        />
                      </span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </nav>
        )}
        {showDetail && (
          <div className="min-w-0 px-3 py-6 min-[761px]:px-6 min-[761px]:py-7">
            {!desktop && (
              <Link
                className="-mx-2 mb-4 inline-flex min-h-9 items-center gap-1 rounded-md px-2 text-sm hover:bg-surface"
                to={link({ pane: null })}
              >
                <ChevronLeft
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
                Outline
              </Link>
            )}
            {section === "details" && (
              <div className="max-w-180">
                <TitleForm album={album} />
                <PublishChecklist album={album} />
              </div>
            )}
            {section === "access" && (
              <AlbumAccessSection album={album} className="max-w-180" />
            )}
            {section === "preview" && <ViewerPreviewSection album={album} />}
            {section === "moments" &&
              (moment ? (
                <MomentPane
                  album={album}
                  dialogs={dialogs}
                  key={moment.id}
                  moment={moment}
                />
              ) : (
                <p className="text-sm text-muted">
                  This Album had no photos or videos to import.
                </p>
              ))}
          </div>
        )}
      </div>
      {dialogs.element}
    </>
  );
}

// Who can see the Moment, left aligned under its title. Hover or focus lists
// everyone; the count of suggestions sits beside the stack.
function OutlineAudience({
  people,
  suggestions,
}: {
  people: PrototypeAccessPerson[];
  suggestions: number;
}) {
  const allowed = people.filter((person) => person.decision === "allow");
  const shown = allowed.slice(0, 4);
  const overflow = allowed.length - shown.length;
  const names = allowed.map((person) => person.display_name).join(", ");
  return (
    <span className="mt-1.5 flex items-center gap-2 text-xs">
      {allowed.length === 0 ? (
        <span className="text-muted">No access yet</span>
      ) : (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                aria-label={`Allowed: ${names}`}
                className="flex cursor-default items-center -space-x-1.5 rounded-full"
                role="img"
                tabIndex={0}
              >
                {shown.map((person) => (
                  <PersonAvatar
                    className="size-6 border-2 border-background"
                    key={person.person_id}
                    person={person}
                  />
                ))}
                {overflow > 0 && (
                  <span className="flex size-6 items-center justify-center rounded-full border-2 border-background bg-surface text-[10px] font-medium">
                    +{overflow}
                  </span>
                )}
              </span>
            </TooltipTrigger>
            <TooltipContent align="start" side="bottom">
              <ul className="space-y-1">
                {allowed.map((person) => (
                  <li key={person.person_id}>{person.display_name}</li>
                ))}
              </ul>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
      {suggestions > 0 && (
        <span className="text-accent-foreground">{suggestions} suggested</span>
      )}
    </span>
  );
}

function MomentPane({
  album,
  moment,
  dialogs,
}: {
  album: PrototypeAlbum;
  moment: PrototypeMoment;
  dialogs: MomentDialogs;
}) {
  const { params, update } = useURLState();
  const selection = useSelection();
  const heading = momentHeading(moment);
  const all = params.get("media") === "all";
  const visible = all ? moment.entries : moment.entries.slice(0, 24);
  const selecting = selection.momentID === moment.id;
  const selected = selecting ? selection.entryIDs : [];
  const refresh = useRefreshMomentFaces(album.id, moment.id);
  const run = refresh.mutate;
  useEffect(() => {
    run();
  }, [run]);
  return (
    <section aria-labelledby={`pane-${moment.id}`}>
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h2 className={sectionHeadingClass} id={`pane-${moment.id}`}>
            {heading.title}
          </h2>
          <p className="mt-1 text-xs text-muted">
            {heading.date && <span className="mr-3">{heading.date}</span>}
            {mediaCounts(moment.entries)}
          </p>
        </div>
        <div className="flex flex-wrap gap-1">
          <Button
            onClick={() => dialogs.rename(moment)}
            size="sm"
            variant="ghost"
          >
            Rename
          </Button>
          <Button
            disabled={album.moments.length < 2}
            onClick={() => dialogs.structure(moment, "merge", [])}
            size="sm"
            variant="ghost"
          >
            Merge
          </Button>
        </div>
      </header>
      <div className="mt-5 rounded-md border border-border p-4">
        <AccessInspector
          album={album}
          heading={false}
          layout="strip"
          moment={moment}
          onRules={() => dialogs.rules(moment)}
          refresh={{
            pending: refresh.isPending,
            error: refresh.error,
            run: () => run(),
          }}
        />
      </div>
      <form
        aria-label={`Select media in ${moment.label}`}
        className="mt-6"
        onSubmit={(event) => event.preventDefault()}
      >
        <div className="flex min-h-10 flex-wrap items-center justify-between gap-3">
          {selecting ? (
            <label className="flex cursor-pointer items-center gap-2 text-xs">
              <input
                aria-label={`Select all ${moment.entries.length} items`}
                checked={
                  moment.entries.length > 0 &&
                  selected.length === moment.entries.length
                }
                className="size-4 cursor-pointer accent-primary"
                onChange={(event) =>
                  selection.set(
                    moment.id,
                    event.target.checked
                      ? moment.entries.map((entry) => entry.id)
                      : [],
                  )
                }
                type="checkbox"
              />
              {selected.length ? `${selected.length} selected` : "Select all"}
            </label>
          ) : (
            <p className="text-xs text-muted">
              {visible.length} of{" "}
              {countLabel(moment.entries.length, "item", "items")} shown
            </p>
          )}
          <div className="flex flex-wrap items-center gap-1">
            {selecting ? (
              <SelectionActions
                album={album}
                dialogs={dialogs}
                moment={moment}
                selection={selection}
              />
            ) : (
              <>
                {moment.entries.length > 24 && (
                  <Button
                    className="text-xs"
                    onClick={() => update({ media: all ? null : "all" })}
                    size="sm"
                    variant="ghost"
                  >
                    {all ? "Show fewer" : `Show all ${moment.entries.length}`}
                  </Button>
                )}
                <Button
                  onClick={() => selection.start(moment.id)}
                  size="sm"
                  variant="outline"
                >
                  Select
                </Button>
              </>
            )}
          </div>
        </div>
        <ul
          aria-label="Moment media"
          className="mt-3 flex flex-wrap items-start gap-x-2 gap-y-4"
        >
          {visible.map((entry) => (
            <EntryPreview
              cover={entry.id === moment.cover_entry_id}
              entry={entry}
              key={entry.id}
              onSelect={
                selecting
                  ? (checked) => selection.toggle(moment.id, entry.id, checked)
                  : undefined
              }
              selected={selected.includes(entry.id)}
            />
          ))}
        </ul>
      </form>
    </section>
  );
}
