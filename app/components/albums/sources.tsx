import {
  CalendarDays,
  ChevronRight,
  CircleCheck,
  FolderOpen,
  SearchX,
} from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";

import { useImportAlbum, useSources } from "../../hooks/queries/albums";
import { useConnection } from "../../hooks/queries/connection";
import { cn } from "../../lib/utils";
import type {
  AlbumDetail,
  SourceAlbum,
} from "../../types/generated/publishing";
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

export function ImportPage() {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const requestedPage = Number(params.get("page") ?? 1);
  const page =
    Number.isSafeInteger(requestedPage) && requestedPage > 0
      ? requestedPage
      : 1;
  const sources = useSources(search, page);
  const importing = useImportAlbum();
  const connection = useConnection("curator");
  const importDisabled =
    importing.isPending || connection.data?.import_supported === false;
  const navigate = useNavigate();
  const pageLink = (next: number) =>
    `?${new URLSearchParams({ q: search, page: String(next) })}`;
  return (
    <>
      <PageTitle title="Import an album" />
      <BackLink to="/curator/albums">All albums</BackLink>
      <h1 className={headingClass}>Import an album</h1>
      <p className="mt-5 max-w-150 text-muted">
        Choose an Immich album to bring into Memento. It stays unpublished until
        you choose to share it.
      </p>
      <SearchForm
        className="mt-9 mb-7"
        label="Search Immich albums"
        onSearch={(value) => setParams({ q: value.trim(), page: "1" })}
        value={search}
      />
      <ImportWarning />
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
                icon={search ? SearchX : FolderOpen}
                title={search ? "No matching albums" : "No Immich albums yet"}
              >
                {search
                  ? "Try another search."
                  : "Create an album in Immich, then come back to import it."}
              </EmptyState>
            ) : (
              <div className="grid grid-cols-2 gap-x-5 gap-y-8 min-[601px]:grid-cols-3 min-[1001px]:grid-cols-4 min-[1401px]:grid-cols-6">
                {sources.data.albums.map((source) => (
                  <SourceCard
                    disabled={importDisabled}
                    importing={importing}
                    key={source.id}
                    onImported={(album) =>
                      void navigate(`/curator/albums/${album.id}`)
                    }
                    source={source}
                  />
                ))}
              </div>
            )}
            {sources.data.pages > 1 && (
              <nav
                aria-label="Album pages"
                className="mt-10 flex flex-wrap items-center gap-4"
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
                    <Link to={pageLink(sources.data.page + 1)}>Next page</Link>
                  </Button>
                )}
              </nav>
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

// One Immich album as a card: cover, title, size and dates, and a footer
// that says where it stands. An album already in Memento is a link to it; one
// that is not yet has its Import action on the left, with the right side of
// the footer kept for what else can happen to a source later.
function SourceCard({
  source,
  importing,
  disabled,
  onImported,
}: {
  source: SourceAlbum;
  importing: ReturnType<typeof useImportAlbum>;
  disabled: boolean;
  onImported: (album: AlbumDetail) => void;
}) {
  const pending =
    importing.isPending && importing.variables?.source_id === source.id;
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
          <p className="mt-1 flex items-center gap-1 text-xs/5 text-muted">
            <CalendarDays
              aria-hidden="true"
              className="size-3.5 shrink-0"
              strokeWidth={1.5}
            />
            {sourceDates(source)}
          </p>
        )}
        <p className="text-xs/5 text-muted">
          {source.count} {source.count === 1 ? "item" : "items"}
        </p>
      </div>
    </>
  );
  const cardClass =
    "flex min-w-0 flex-col overflow-hidden rounded-lg border border-border bg-surface";
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
      <Form
        aria-busy={pending}
        aria-label={`Import ${source.title}`}
        className="flex min-h-12 items-center justify-between gap-2 border-t border-border px-3 py-2"
        error={
          importing.variables?.source_id === source.id ? importing.error : null
        }
        onSubmit={(event) => {
          event.preventDefault();
          if (!disabled)
            importing.mutate(
              { source_id: source.id },
              { onSuccess: onImported },
            );
        }}
      >
        <Button disabled={disabled} size="sm" type="submit">
          {pending ? "Importing…" : "Import"}
        </Button>
      </Form>
    </article>
  );
}
