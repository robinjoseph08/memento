import { Link, useNavigate, useSearchParams } from "react-router-dom";

import { useImportAlbum, useSources } from "../../hooks/queries/albums";
import { useConnection } from "../../hooks/queries/connection";
import { SearchForm } from "../forms/search-form";
import {
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { BackLink } from "../shell/back-link";
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
      <BackLink to="/curator">All albums</BackLink>
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
              <section className="border-t border-border py-9">
                <h2 className={sectionHeadingClass}>
                  {search ? "No matching albums" : "No Immich albums yet"}
                </h2>
                <p className="mt-4 text-muted">
                  {search
                    ? "Try another search."
                    : "Create an album in Immich, then come back to import it."}
                </p>
              </section>
            ) : (
              <div className="grid gap-x-8 gap-y-7 min-[1001px]:grid-cols-2">
                {sources.data.albums.map((source) => (
                  <article
                    className="grid min-w-0 grid-cols-[80px_minmax(0,1fr)] items-start gap-4 border-t border-border pt-5 min-[601px]:grid-cols-[112px_minmax(0,1fr)]"
                    key={source.id}
                  >
                    <AlbumImage
                      alt={source.title}
                      className="mx-auto h-auto max-h-40 w-auto max-w-full"
                      fallback="No cover available"
                      src={source.cover_url}
                    />
                    <div className="min-w-0">
                      <h2 className="font-heading text-xl break-words">
                        {source.title}
                      </h2>
                      <p className="mt-2 text-sm text-muted">
                        {source.count} {source.count === 1 ? "item" : "items"}
                      </p>
                      {(source.start_date || source.end_date) && (
                        <p className="mt-1 text-xs text-muted">
                          {[
                            dateLabel(source.start_date),
                            dateLabel(source.end_date),
                          ]
                            .filter(Boolean)
                            .filter(
                              (date, index, dates) =>
                                dates.indexOf(date) === index,
                            )
                            .join(" to ")}
                        </p>
                      )}
                      <div className="mt-4">
                        {source.album_id ? (
                          <Button asChild variant="outline">
                            <Link to={`/curator/albums/${source.album_id}`}>
                              Open album
                            </Link>
                          </Button>
                        ) : (
                          <Form
                            aria-busy={
                              importing.isPending &&
                              importing.variables?.source_id === source.id
                            }
                            aria-label={`Import ${source.title}`}
                            error={
                              importing.variables?.source_id === source.id
                                ? importing.error
                                : null
                            }
                            onSubmit={(event) => {
                              event.preventDefault();
                              if (!importDisabled)
                                importing.mutate(
                                  { source_id: source.id },
                                  {
                                    onSuccess: (album) =>
                                      void navigate(
                                        `/curator/albums/${album.id}`,
                                      ),
                                  },
                                );
                            }}
                          >
                            <Button disabled={importDisabled} type="submit">
                              {importing.isPending &&
                              importing.variables?.source_id === source.id
                                ? "Importing…"
                                : "Import"}
                            </Button>
                          </Form>
                        )}
                      </div>
                    </div>
                  </article>
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
