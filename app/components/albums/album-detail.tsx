import {
  Ban,
  Eye,
  EyeOff,
  Glasses,
  Image,
  Info,
  KeyRound,
  RefreshCw,
  Rocket,
  type LucideIcon,
} from "lucide-react";
import { use, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { useAlbum, useUpdateAlbum } from "../../hooks/queries/albums";
import { useMediaQuery } from "../../hooks/use-media-query";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { UnsavedChangesContext } from "../../lib/forms";
import { fieldErrors } from "../../lib/http";
import { cn, withParams } from "../../lib/utils";
import type { AlbumDetail } from "../../types/generated/publishing";
import { ConfirmDialog } from "../forms/confirm-dialog";
import {
  Field,
  Form,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { BackLink } from "../shell/back-link";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { Textarea } from "../ui/textarea";
import { ViewerPreview } from "../viewer/viewer-preview";
import { AlbumAccess } from "./album-access";
import { AlbumCover } from "./album-cover";
import { AlbumImage } from "./album-image";
import { DangerZone } from "./album-lifecycle";
import { Audience } from "./audience";
import { CoverWash } from "./cover-wash";
import { ExcludedPane } from "./exclusions";
import { ImportProgress } from "./import-progress";
import { MediaCounts } from "./media-counts";
import {
  countLabel,
  countMedia,
  momentCover,
  momentHeading,
  shortDay,
} from "./moment-labels";
import { MomentPane } from "./moments";
import { PublishChecklist, PublishDialog } from "./publication-review";
import { SyncDialog } from "./sync-review";

const outlineRowClass = (active: boolean) =>
  cn(
    "group flex min-h-11 items-center gap-3 rounded-sm border-l-2 px-3 text-left hover:bg-surface",
    active ? "border-primary bg-surface text-foreground" : "border-transparent",
  );

type Section = "details" | "access" | "cover" | "preview" | "excluded";

// The Album-level sections of the outline, above the Moments.
const sections: { key: Section; label: string; icon: LucideIcon }[] = [
  { key: "details", label: "Album details", icon: Info },
  { key: "access", label: "Album access", icon: KeyRound },
  { key: "cover", label: "Album cover", icon: Image },
  { key: "preview", label: "Viewer preview", icon: Glasses },
  { key: "excluded", label: "Excluded media", icon: Ban },
];

export function AlbumPage() {
  const { id = "" } = useParams();
  const album = useAlbum(id);
  return (
    <>
      <PageTitle title={album.data?.title ?? "Album"} />
      {album.isPending && (
        <div className="px-5 py-4 min-[761px]:px-8">
          <BackLink className="mb-1 min-h-7 text-xs" to="/curator/albums">
            All albums
          </BackLink>
          <h1 className={sectionHeadingClass}>Album</h1>
          <p className="mt-4" role="status">
            Loading album…
          </p>
        </div>
      )}
      {album.isError && (
        <div className="px-5 py-4">
          <ReadFailure
            error={album.error}
            pending={album.isFetching}
            retry={album.refetch}
          />
        </div>
      )}
      {album.data && <AlbumContent album={album.data} key={id} />}
    </>
  );
}

// Master-detail: a header, a left outline of the Album sections and every
// Moment with its audience, and the selected section or Moment on the right.
// On narrow screens the outline is the first screen and rows drill into the
// pane with a back link.
function AlbumContent({ album }: { album: AlbumDetail }) {
  const [params] = useSearchParams();
  const [publicationOpen, setPublicationOpen] = useState(false);
  const [syncOpen, setSyncOpen] = useState(false);
  // Publishing and a sync check use saved state, so unsaved work in any
  // section gets the same prompt as leaving the page.
  const unsaved = use(UnsavedChangesContext);
  const [guarded, setGuarded] = useState<"publish" | "sync" | null>(null);
  const openDialog = (action: "publish" | "sync") =>
    action === "publish" ? setPublicationOpen(true) : setSyncOpen(true);
  const guard = (action: "publish" | "sync") => {
    if ((unsaved?.current.size ?? 0) > 0) setGuarded(action);
    else openDialog(action);
  };
  const desktop = useMediaQuery("(min-width: 761px)");
  const requested = sections.find((item) => item.key === params.get("section"));
  const section = requested?.key ?? "moments";
  const moment =
    album.moments.find((item) => item.id === params.get("moment")) ??
    album.moments[0];
  const drilled = params.get("pane") === "detail";
  const showOutline = desktop || !drilled;
  const showDetail = desktop || drilled;
  const link = (values: Record<string, string | null>) =>
    `?${withParams(params, values)}`;
  const complete = album.status === "complete";
  return (
    <>
      <header className="relative isolate flex flex-wrap items-center gap-4 overflow-hidden border-b border-border px-5 py-4 min-[761px]:px-8">
        <CoverWash src={album.cover_url} />
        <div className="min-w-0 flex-1">
          <BackLink className="mb-1 min-h-7 text-xs" to="/curator/albums">
            All albums
          </BackLink>
          <h1 className="font-heading text-[clamp(24px,3vw,30px)] leading-tight tracking-[-0.5px] wrap-anywhere">
            {album.title}
          </h1>
          <p className="mt-1 text-xs text-muted">
            {complete ? (
              <MediaCounts
                {...countMedia(album.moments.flatMap((item) => item.entries))}
              />
            ) : (
              <span>{countLabel(album.total, "item", "items")}</span>
            )}
            <span className="ml-3 inline-flex items-center gap-1 border-l border-border pl-3 align-bottom">
              {album.published ? (
                <Eye
                  aria-hidden="true"
                  className="size-3.5 text-accent-foreground"
                  strokeWidth={1.5}
                />
              ) : (
                <EyeOff
                  aria-hidden="true"
                  className="size-3.5"
                  strokeWidth={1.5}
                />
              )}
              {album.published ? "Published" : "Unpublished"}
            </span>
          </p>
        </div>
        <div className="flex w-full flex-wrap gap-2 min-[761px]:w-auto">
          <Button
            className="flex-1 min-[761px]:flex-none"
            disabled={!complete}
            onClick={() => guard("sync")}
            variant="outline"
          >
            <RefreshCw
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            Check for changes
          </Button>
          {!album.published && (
            <Button
              className="flex-1 min-[761px]:flex-none"
              disabled={!complete}
              onClick={() => guard("publish")}
            >
              <Rocket aria-hidden="true" className="size-4" strokeWidth={1.5} />
              Review & publish
            </Button>
          )}
        </div>
      </header>
      <ConfirmDialog
        confirmLabel="Continue"
        description="You have unsaved changes on this Album. They will not be included until you save them."
        onConfirm={() => {
          const action = guarded;
          setGuarded(null);
          if (action) openDialog(action);
        }}
        onOpenChange={(open) => {
          if (!open) setGuarded(null);
        }}
        open={guarded !== null}
        title="Continue without saving?"
      />
      {publicationOpen && (
        <PublishDialog
          album={album}
          onClose={() => setPublicationOpen(false)}
        />
      )}
      {syncOpen && (
        <SyncDialog album={album} onClose={() => setSyncOpen(false)} />
      )}
      {complete ? (
        <div className="min-[761px]:grid min-[761px]:min-h-[70vh] min-[761px]:grid-cols-[300px_minmax(0,1fr)]">
          {showOutline && (
            <nav
              aria-label="Album outline"
              className="px-3 py-5 min-[761px]:sticky min-[761px]:top-0 min-[761px]:max-h-dvh min-[761px]:overflow-y-auto min-[761px]:border-r min-[761px]:border-border"
            >
              <p className="px-3 text-xs text-muted">Album</p>
              <ul className="mt-1 space-y-0.5">
                {sections.map((item) => (
                  <li key={item.key}>
                    <Link
                      aria-current={section === item.key ? "page" : undefined}
                      className={cn(
                        outlineRowClass(section === item.key),
                        "text-sm",
                      )}
                      to={link({
                        section: item.key,
                        pane: "detail",
                        entry: null,
                      })}
                    >
                      <item.icon
                        aria-hidden="true"
                        className="size-4 shrink-0 text-muted group-aria-[current=page]:text-accent-foreground"
                        strokeWidth={1.5}
                      />
                      {item.label}
                      {item.key === "excluded" && (
                        <span className="ml-auto text-xs text-accent-foreground">
                          {album.excluded.length}
                        </span>
                      )}
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
              {album.moments.length === 0 ? (
                // The pane carries this explanation on wide screens.
                <p className="mt-2 px-3 text-xs text-muted min-[761px]:hidden">
                  This Album had no photos or videos to import.
                </p>
              ) : (
                <ul className="mt-1 space-y-0.5">
                  {album.moments.map((item) => {
                    const heading = momentHeading(item);
                    const cover = momentCover(item);
                    const day = shortDay(item.date);
                    const active =
                      section === "moments" && item.id === moment?.id;
                    const suggestions = item.access.people.filter(
                      (person) => person.suggested,
                    ).length;
                    return (
                      <li key={item.id}>
                        <Link
                          aria-current={active ? "page" : undefined}
                          className={cn(
                            outlineRowClass(active),
                            "items-start py-2.5",
                          )}
                          to={link({
                            section: null,
                            moment: item.id,
                            pane: "detail",
                            media: null,
                            entry: null,
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
                            <span className="flex flex-wrap items-center gap-x-3 text-xs text-muted">
                              {day && <span>{day}</span>}
                              <MediaCounts {...countMedia(item.entries)} />
                            </span>
                            <span className="mt-1.5 block">
                              <Audience
                                people={item.access.people.filter(
                                  (person) => person.accessible_count > 0,
                                )}
                                suggestions={suggestions}
                              />
                            </span>
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
            </nav>
          )}
          <div
            className="min-w-0 px-3 py-6 min-[761px]:px-6 min-[761px]:py-7"
            hidden={!showDetail}
          >
            {!desktop && (
              <BackLink className="mb-4" to={link({ pane: null })}>
                Outline
              </BackLink>
            )}
            {/* The title form stays mounted while other sections show so an
                unsaved edit survives a look at a Moment. */}
            <div className="max-w-180" hidden={section !== "details"}>
              <TitleForm album={album} />
              {!album.published && <PublishChecklist album={album} />}
              <DangerZone album={album} />
            </div>
            {showDetail && section === "access" && (
              <AlbumAccess album={album} />
            )}
            {showDetail && section === "cover" && <AlbumCover album={album} />}
            {showDetail && section === "preview" && (
              <ViewerPreview album={album} />
            )}
            {showDetail && section === "excluded" && (
              <ExcludedPane album={album} />
            )}
            {showDetail &&
              section === "moments" &&
              (moment ? (
                <MomentPane album={album} key={moment.id} moment={moment} />
              ) : (
                <p className="text-sm text-muted">
                  This Album had no photos or videos to import.
                </p>
              ))}
          </div>
        </div>
      ) : (
        <div className="px-5 min-[761px]:px-8">
          <ImportProgress album={album} />
        </div>
      )}
    </>
  );
}

function TitleForm({ album }: { album: AlbumDetail }) {
  const update = useUpdateAlbum(album.id);
  const [draft, setDraft] = useState<string | null>(null);
  const title = draft ?? album.title;
  const dirty = (draft !== null && draft !== album.title) || update.isPending;
  useUnsavedChanges(dirty);
  return (
    <section aria-labelledby="album-details-heading">
      <h2 className={sectionHeadingClass} id="album-details-heading">
        Album details
      </h2>
      <Form
        aria-busy={update.isPending}
        aria-label="Edit album title"
        className="mt-7"
        error={update.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (!update.isPending)
            update.mutate({ title }, { onSuccess: () => setDraft(null) });
        }}
      >
        <fieldset disabled={update.isPending}>
          <Field
            className="bg-surface"
            error={fieldErrors(update.error).title}
            label="Album title"
            maxLength={200}
            name="title"
            onChange={(event) => {
              update.reset();
              setDraft(event.target.value);
            }}
            required
            value={title}
          />
          <div className="mb-6">
            <label
              className="mb-2 block text-xs font-medium"
              htmlFor="source-description"
            >
              Description from Immich
            </label>
            <Textarea
              className="bg-surface"
              id="source-description"
              placeholder="No description in Immich."
              readOnly
              value={album.description}
            />
            <p className="mt-3 text-xs text-muted">
              Read-only. This is the description captured when the Album was
              imported.
            </p>
          </div>
          <Button type="submit">
            {update.isPending ? "Saving…" : "Save title"}
          </Button>
        </fieldset>
        {update.isSuccess && (
          <p className="mt-4 text-sm text-muted" role="status">
            Title saved.
          </p>
        )}
      </Form>
      <div className="mt-9 border-t border-border pt-6">
        <p className="text-sm">
          {!album.published
            ? "Only Curators can see this unpublished Album."
            : "Album details are managed by Curators."}
        </p>
      </div>
    </section>
  );
}
