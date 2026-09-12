import { ChevronLeft } from "lucide-react";
import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { useAlbum, useUpdateAlbum } from "../../hooks/queries/albums";
import { useMediaQuery } from "../../hooks/use-media-query";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { cn, withParams } from "../../lib/utils";
import type { AlbumDetail } from "../../types/generated/publishing";
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
import { AlbumImage } from "./album-image";
import { Audience } from "./audience";
import { ImportProgress } from "./import-progress";
import {
  countLabel,
  mediaCounts,
  momentCover,
  momentHeading,
  shortDay,
} from "./moment-labels";
import { MomentPane } from "./moments";

const outlineRowClass = (active: boolean) =>
  cn(
    "flex min-h-11 items-center gap-3 rounded-sm border-l-2 px-3 text-left hover:bg-surface",
    active ? "border-primary bg-surface text-foreground" : "border-transparent",
  );

export function AlbumPage() {
  const { id = "" } = useParams();
  const album = useAlbum(id);
  return (
    <>
      <PageTitle title={album.data?.title ?? "Album"} />
      {album.isPending && (
        <div className="px-5 py-4 min-[761px]:px-8">
          <BackLink className="mb-2 text-xs" to="/curator">
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
  const desktop = useMediaQuery("(min-width: 761px)");
  const section = params.get("section") === "details" ? "details" : "moments";
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
      <header className="border-b border-border px-5 py-4 min-[761px]:px-8">
        <BackLink className="mb-2 text-xs" to="/curator">
          All albums
        </BackLink>
        <h1 className="font-heading text-[clamp(24px,3vw,30px)] leading-tight tracking-[-0.5px] wrap-anywhere">
          {album.title}
        </h1>
        <p className="mt-1 text-xs text-muted">
          <span>
            {complete
              ? mediaCounts(album.moments.flatMap((item) => item.entries))
              : countLabel(album.total, "item", "items")}
          </span>
          {!album.published && (
            <span className="ml-3 border-l border-border pl-3">
              Unpublished
            </span>
          )}
        </p>
      </header>
      {complete ? (
        <div className="min-[761px]:grid min-[761px]:min-h-[70vh] min-[761px]:grid-cols-[300px_minmax(0,1fr)]">
          {showOutline && (
            <nav
              aria-label="Album outline"
              className="px-3 py-5 min-[761px]:sticky min-[761px]:top-0 min-[761px]:max-h-dvh min-[761px]:overflow-y-auto min-[761px]:border-r min-[761px]:border-border"
            >
              <p className="px-3 text-xs text-muted">Album</p>
              <ul className="mt-1 space-y-0.5">
                <li>
                  <Link
                    aria-current={section === "details" ? "page" : undefined}
                    className={cn(
                      outlineRowClass(section === "details"),
                      "text-sm",
                    )}
                    to={link({ section: "details", pane: "detail" })}
                  >
                    Album details
                  </Link>
                </li>
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
                              {day && `${day}, `}
                              {mediaCounts(item.entries)}
                            </span>
                            <span className="mt-1.5 block">
                              <Audience
                                people={item.access.people}
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
            {/* The title form stays mounted while other sections show so an
                unsaved edit survives a look at a Moment. */}
            <div className="max-w-180" hidden={section !== "details"}>
              <TitleForm album={album} />
            </div>
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
  useUnsavedChanges(
    (draft !== null && draft !== album.title) || update.isPending,
  );
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
