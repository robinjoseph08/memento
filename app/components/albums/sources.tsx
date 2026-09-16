import { useIsMutating } from "@tanstack/react-query";
import {
  ArchiveRestore,
  CalendarDays,
  ChevronRight,
  CircleCheck,
  EyeOff,
  FolderOpen,
  Images,
  SearchX,
} from "lucide-react";
import type { ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";

import {
  useIgnoreSource,
  useImportAlbum,
  useRestoreSource,
  useSources,
} from "../../hooks/queries/albums";
import { useConnection } from "../../hooks/queries/connection";
import { usePrivateScope } from "../../hooks/queries/people";
import { cn } from "../../lib/utils";
import type { SourceAlbum } from "../../types/generated/publishing";
import { SearchForm } from "../forms/search-form";
import { Form, headingClass, ReadFailure } from "../people/form-fields";
import { BackLink } from "../shell/back-link";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";
import { ImportWarning } from "./import-warning";

function dateLabel(value: string) {
  const date = new Date(`${value.slice(0, 10)}T12:00:00`);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        year: "numeric",
      });
}

// The import page lists the Immich albums on offer; with ignored it lists the
// ones a Curator keeps off that list instead, each with a way back.
export function ImportPage({ ignored = false }: { ignored?: boolean }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const requestedPage = Number(params.get("page") ?? 1);
  const page =
    Number.isSafeInteger(requestedPage) && requestedPage > 0
      ? requestedPage
      : 1;
  const sources = useSources(search, page, ignored);
  const connection = useConnection("curator");
  // Every card waits while any Curator mutation is in flight, so one click
  // cannot start a second import or ignore before the first has landed.
  const scope = usePrivateScope();
  const busy = useIsMutating({ mutationKey: scope }) > 0;
  const importSupported = connection.data?.import_supported !== false;
  const title = ignored ? "Ignored albums" : "Import an album";
  // The way to the ignored list sits at the foot of the offered list, out of
  // the way; it is rarely needed and never on the ignored page itself.
  const ignoredCount = ignored ? 0 : (sources.data?.ignored ?? 0);
  const pageLink = (next: number) =>
    `?${new URLSearchParams({ q: search, page: String(next) })}`;
  return (
    <>
      <PageTitle title={title} />
      {ignored ? (
        <BackLink to="/curator/import">Import an album</BackLink>
      ) : (
        <BackLink to="/curator/albums">All albums</BackLink>
      )}
      <h1 className={headingClass}>{title}</h1>
      <p className="mt-5 max-w-150 text-muted">
        {ignored
          ? "These Immich albums stay off the import list. Restore one to offer it again."
          : "Choose an Immich album to bring into Memento. It stays unpublished until you choose to share it."}
      </p>
      <SearchForm
        className="mt-9 mb-7"
        label={ignored ? "Search ignored albums" : "Search Immich albums"}
        onSearch={(value) => setParams({ q: value.trim(), page: "1" })}
        value={search}
      />
      {!ignored && <ImportWarning />}
      <div>
        {sources.isPending && <p role="status">Loading Immich albums…</p>}
        {sources.isError && (
          <ReadFailure
            error={sources.error}
            pending={sources.isFetching}
            retry={sources.refetch}
          />
        )}
        {sources.data && (
          <>
            {sources.data.albums.length === 0 ? (
              <EmptyState
                icon={
                  search
                    ? SearchX
                    : ignored || ignoredCount
                      ? EyeOff
                      : FolderOpen
                }
                title={
                  search
                    ? "No matching albums"
                    : ignored
                      ? "No ignored albums"
                      : ignoredCount
                        ? "Every Immich album is ignored"
                        : "No Immich albums yet"
                }
              >
                {search
                  ? "Try another search."
                  : ignored
                    ? "Ignore an album on the import page and it will show up here."
                    : ignoredCount
                      ? "Restore one from the ignored list to import it."
                      : "Create an album in Immich, then come back to import it."}
              </EmptyState>
            ) : (
              <div className="grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4 min-[1401px]:grid-cols-6">
                {sources.data.albums.map((source) => (
                  <SourceCard key={source.id} source={source}>
                    {ignored ? (
                      <RestoreAction disabled={busy} source={source} />
                    ) : (
                      <>
                        <ImportAction
                          disabled={busy || !importSupported}
                          source={source}
                        />
                        <IgnoreAction disabled={busy} source={source} />
                      </>
                    )}
                  </SourceCard>
                ))}
              </div>
            )}
            {(sources.data.pages > 1 || ignoredCount > 0) && (
              <div className="mt-10 flex flex-wrap items-center gap-4">
                {sources.data.pages > 1 && (
                  <nav
                    aria-label="Album pages"
                    className="flex flex-wrap items-center gap-4"
                  >
                    {sources.data.page > 1 && (
                      <Button asChild variant="outline">
                        <Link to={pageLink(sources.data.page - 1)}>
                          Previous page
                        </Link>
                      </Button>
                    )}
                    <p className="text-sm text-muted">
                      Page {sources.data.page} of {sources.data.pages}
                    </p>
                    {sources.data.page < sources.data.pages && (
                      <Button asChild variant="outline">
                        <Link to={pageLink(sources.data.page + 1)}>
                          Next page
                        </Link>
                      </Button>
                    )}
                  </nav>
                )}
                {ignoredCount > 0 && (
                  <Link
                    className="-mr-2 ml-auto inline-flex min-h-8 items-center gap-1.5 rounded-md px-2 text-sm text-muted hover:bg-surface hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
                    to="/curator/import/ignored"
                  >
                    <EyeOff
                      aria-hidden="true"
                      className="size-4"
                      strokeWidth={1.5}
                    />
                    {ignoredCount}{" "}
                    {ignoredCount === 1 ? "ignored album" : "ignored albums"}
                  </Link>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </>
  );
}

function sourceDates(source: SourceAlbum) {
  return [dateLabel(source.start_date), dateLabel(source.end_date)]
    .filter(Boolean)
    .filter((date, index, dates) => dates.indexOf(date) === index)
    .join(" to ");
}

const cardClass =
  "flex min-w-0 flex-col overflow-hidden rounded-lg border border-border bg-surface";
// Each footer action is its own small form so it owns its error and its
// accessible name. Forms sit side by side and an error wraps under its button.
const actionClass =
  "flex min-w-0 flex-col justify-center [&>p]:mx-1 [&>p]:my-2 [&>p]:text-pretty";

// One Immich album as a card: cover, title, size and dates, and a footer
// that says where it stands. An album already in Memento is a link to it; one
// that is not yet shows the actions the page passes as children.
function SourceCard({
  source,
  children,
}: {
  source: SourceAlbum;
  children: ReactNode;
}) {
  const details = (
    <>
      <AlbumImage
        alt={source.title}
        className="aspect-square h-auto w-full rounded-none object-cover"
        fallback="No cover available"
        src={source.cover_url}
      />
      <div className="min-w-0 flex-1 px-4 pt-3 pb-4">
        <h2 className="font-heading text-lg break-words">{source.title}</h2>
        {sourceDates(source) && (
          <p className="mt-1 flex items-start gap-1 text-xs/5 text-muted">
            <CalendarDays
              aria-hidden="true"
              className="mt-[3px] size-3.5 shrink-0"
              strokeWidth={1.5}
            />
            {sourceDates(source)}
          </p>
        )}
        <p className="flex items-start gap-1 text-xs/5 text-muted">
          <Images
            aria-hidden="true"
            className="mt-[3px] size-3.5 shrink-0"
            strokeWidth={1.5}
          />
          {source.count} {source.count === 1 ? "item" : "items"}
        </p>
      </div>
    </>
  );
  if (source.album_id)
    return (
      <article className="min-w-0">
        <Link
          aria-label={`Open album ${source.title}`}
          className={cn(
            cardClass,
            "h-full cursor-pointer hover:border-muted focus-visible:outline-2 focus-visible:outline-ring",
          )}
          to={`/curator/albums/${source.album_id}`}
        >
          {details}
          <span className="flex min-h-12 items-center justify-between gap-2 border-t border-border px-4 text-sm">
            <span className="inline-flex items-center gap-1.5 text-accent-foreground">
              <CircleCheck
                aria-hidden="true"
                className="size-4"
                strokeWidth={1.5}
              />
              In Memento
            </span>
            <ChevronRight
              aria-hidden="true"
              className="size-4 text-muted"
              strokeWidth={1.5}
            />
          </span>
        </Link>
      </article>
    );
  return (
    <article className={cn(cardClass, "h-full")}>
      {details}
      <div className="flex min-h-12 items-start justify-between gap-2 border-t border-border px-3 py-2">
        {children}
      </div>
    </article>
  );
}

// Import is the main thing to do with a source. Navigation waits on the
// mutation's promise rather than on this card, which leaves the page once
// the source list refetches as imported.
function ImportAction({
  source,
  disabled,
}: {
  source: SourceAlbum;
  disabled: boolean;
}) {
  const navigate = useNavigate();
  const importing = useImportAlbum();
  return (
    <Form
      aria-busy={importing.isPending}
      aria-label={`Import ${source.title}`}
      className={actionClass}
      error={importing.error}
      onSubmit={(event) => {
        event.preventDefault();
        if (disabled) return;
        void importing.mutateAsync({ source_id: source.id }).then(
          (album) => navigate(`/curator/albums/${album.id}`),
          () => undefined,
        );
      }}
    >
      <Button disabled={disabled} size="sm" type="submit">
        {importing.isPending ? "Importing…" : "Import"}
      </Button>
    </Form>
  );
}

// Ignore and Restore keep their pending label until the list has refetched,
// so a card never snaps back before it leaves.
function IgnoreAction({
  source,
  disabled,
}: {
  source: SourceAlbum;
  disabled: boolean;
}) {
  const ignoring = useIgnoreSource();
  return (
    <Form
      aria-busy={ignoring.isPending}
      aria-label={`Ignore ${source.title}`}
      className={cn(actionClass, "items-end")}
      error={ignoring.error}
      onSubmit={(event) => {
        event.preventDefault();
        if (!disabled) ignoring.mutate({ source_id: source.id });
      }}
    >
      <Button
        aria-label={`Ignore ${source.title}`}
        disabled={disabled}
        size="sm"
        type="submit"
        variant="ghost"
      >
        <EyeOff aria-hidden="true" className="size-4" strokeWidth={1.5} />
        {ignoring.isPending ? "Ignoring…" : "Ignore"}
      </Button>
    </Form>
  );
}

function RestoreAction({
  source,
  disabled,
}: {
  source: SourceAlbum;
  disabled: boolean;
}) {
  const restoring = useRestoreSource();
  return (
    <Form
      aria-busy={restoring.isPending}
      aria-label={`Restore ${source.title}`}
      className={actionClass}
      error={restoring.error}
      onSubmit={(event) => {
        event.preventDefault();
        if (!disabled) restoring.mutate({ source_id: source.id });
      }}
    >
      <Button
        aria-label={`Restore ${source.title}`}
        disabled={disabled}
        size="sm"
        type="submit"
        variant="outline"
      >
        <ArchiveRestore
          aria-hidden="true"
          className="size-4"
          strokeWidth={1.5}
        />
        {restoring.isPending ? "Restoring…" : "Restore"}
      </Button>
    </Form>
  );
}
