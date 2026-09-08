import { useState } from "react";
import { Link, useParams } from "react-router-dom";

import {
  useAlbum,
  useRetryAlbum,
  useUpdateAlbum,
} from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { AlbumDetail, Entry } from "../../types/generated/publishing";
import {
  Field,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { AlbumImage } from "./album-image";

export function AlbumPage() {
  const { id = "" } = useParams();
  const album = useAlbum(id);
  return (
    <>
      <PageTitle title={album.data?.title ?? "Album"} />
      <Button asChild className="mb-6" variant="ghost">
        <Link to="/curator">Back to albums</Link>
      </Button>
      {album.isPending && (
        <>
          <h1 className={headingClass}>Album</h1>
          <p className="mt-6" role="status">
            Loading album…
          </p>
        </>
      )}
      {album.isError && (
        <ReadFailure
          error={album.error}
          pending={album.isFetching}
          retry={album.refetch}
        />
      )}
      {album.data && <AlbumContent album={album.data} key={id} />}
    </>
  );
}

function AlbumContent({ album }: { album: AlbumDetail }) {
  return (
    <>
      <h1 className={headingClass}>{album.title}</h1>
      {!album.published && (
        <p className="mt-4 text-sm text-muted">Unpublished</p>
      )}
      {album.description && (
        <div className="mt-6 max-w-160">
          <h2 className="text-sm font-medium">Description from Immich</h2>
          <p className="mt-2 whitespace-pre-wrap text-muted">
            {album.description}
          </p>
        </div>
      )}
      {album.status === "complete" ? (
        <>
          <TitleForm album={album} />
          {album.moments.length ? (
            <div className="mt-10 space-y-12">
              {album.moments.map((moment) => (
                <section
                  aria-labelledby={`moment-${moment.id}`}
                  className="border-t border-border pt-8"
                  key={moment.id}
                >
                  <h2
                    className={sectionHeadingClass}
                    id={`moment-${moment.id}`}
                  >
                    {moment.label}
                  </h2>
                  <MediaGrid
                    entries={moment.entries.filter(
                      (entry) => entry.kind === "IMAGE",
                    )}
                    label="Photos"
                  />
                  <MediaGrid
                    entries={moment.entries.filter(
                      (entry) => entry.kind === "VIDEO",
                    )}
                    label="Videos"
                  />
                </section>
              ))}
            </div>
          ) : (
            <section className="mt-9 border-t border-border py-9">
              <h2 className={sectionHeadingClass}>No photos or videos</h2>
              <p className="mt-4 text-muted">
                This album had no photos or videos to import.
              </p>
            </section>
          )}
        </>
      ) : (
        <ImportProgress album={album} />
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
    <Form
      aria-busy={update.isPending}
      aria-label="Edit album title"
      className="mt-8 max-w-120"
      error={update.error}
      onSubmit={(event) => {
        event.preventDefault();
        if (!update.isPending)
          update.mutate({ title }, { onSuccess: () => setDraft(null) });
      }}
    >
      <fieldset disabled={update.isPending}>
        <Field
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
  );
}

function ImportProgress({ album }: { album: AlbumDetail }) {
  const retry = useRetryAlbum(album.id);
  const labels: Record<string, string> = {
    queued: "Waiting to import",
    processing: "Importing your album",
    interrupted: "Import interrupted",
    failed: "Import failed",
  };
  const active = album.status === "queued" || album.status === "processing";
  return (
    <section className="mt-9 max-w-160 border-t border-border py-8">
      <h2 className={sectionHeadingClass}>
        {labels[album.status] ?? "Import status"}
      </h2>
      <div className="mt-4" role="status">
        <p>
          {album.message ||
            (album.status === "queued"
              ? "Your album is queued. You can leave this page and return later."
              : album.status === "processing"
                ? "Preparing your photos and videos. You can leave this page and return later."
                : "The import did not finish. Try importing this album again.")}
        </p>
        {active && (
          <>
            <p className="mt-3 text-sm text-muted">
              {album.processed} of {album.total} items processed
            </p>
            {album.total > 0 && (
              <progress
                aria-label="Import progress"
                className="mt-3 h-2 w-full accent-primary"
                max={album.total}
                value={album.processed}
              />
            )}
          </>
        )}
      </div>
      {(album.status === "failed" || album.status === "interrupted") && (
        <Form
          aria-busy={retry.isPending}
          aria-label="Retry import"
          className="mt-5"
          error={retry.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!retry.isPending) retry.mutate();
          }}
        >
          <Button disabled={retry.isPending} type="submit">
            {retry.isPending ? "Retrying…" : "Retry import"}
          </Button>
        </Form>
      )}
    </section>
  );
}

function MediaGrid({ entries, label }: { entries: Entry[]; label: string }) {
  if (!entries.length) return null;
  return (
    <div className="mt-6">
      <h3 className="mb-3 text-sm font-medium">{label}</h3>
      <ul
        aria-label={label}
        className="grid grid-cols-2 gap-3 min-[761px]:grid-cols-3 min-[1101px]:grid-cols-4"
      >
        {entries.map((entry) => (
          <li className="min-w-0" key={entry.id}>
            <figure>
              <AlbumImage
                alt={entry.filename}
                fallback={
                  entry.available
                    ? "No preview available"
                    : "Unavailable in Immich"
                }
                src={entry.available ? entry.thumbnail_url : ""}
              />
              <figcaption
                className="mt-2 truncate text-xs text-muted"
                title={entry.filename}
              >
                {entry.filename}
              </figcaption>
            </figure>
          </li>
        ))}
      </ul>
    </div>
  );
}
