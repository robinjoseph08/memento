import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { useAlbum, useUpdateAlbum } from "../../hooks/queries/albums";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { cn } from "../../lib/utils";
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
import { ImportProgress } from "./import-progress";
import { MediaCounts, Moments } from "./moments";

export function AlbumPage() {
  const { id = "" } = useParams();
  const album = useAlbum(id);
  return (
    <>
      <PageTitle title={album.data?.title ?? "Album"} />
      {album.isPending && (
        <div className="px-5 py-6 min-[761px]:px-8">
          <BackLink to="/curator">All albums</BackLink>
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

function AlbumContent({ album }: { album: AlbumDetail }) {
  const [params] = useSearchParams();
  const section = params.get("section") === "details" ? "details" : "moments";
  const sectionLink = (next: string) => {
    const target = new URLSearchParams(params);
    target.set("section", next);
    return `?${target}`;
  };
  const complete = album.status === "complete";
  return (
    <>
      <header className="border-b border-border px-5 py-5 min-[761px]:px-8">
        <BackLink className="mb-3" to="/curator">
          All albums
        </BackLink>
        <h1 className="font-heading text-[clamp(26px,3vw,32px)] leading-tight tracking-[-0.5px] wrap-anywhere">
          {album.title}
        </h1>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted">
          <p>
            {complete ? (
              <MediaCounts
                entries={album.moments.flatMap((moment) => moment.entries)}
              />
            ) : (
              `${album.total} items`
            )}
          </p>
          {!album.published && (
            <span className="border-l border-border pl-4">Unpublished</span>
          )}
        </div>
      </header>
      {complete ? (
        <div className="min-[761px]:grid min-[761px]:min-h-[65vh] min-[761px]:grid-cols-[176px_minmax(0,1fr)]">
          <nav
            aria-label="Album sections"
            className="flex gap-1 border-b border-border px-3 min-[761px]:flex-col min-[761px]:border-r min-[761px]:border-b-0 min-[761px]:py-5"
          >
            {[
              { key: "details", label: "Album details" },
              { key: "moments", label: "Moments" },
            ].map((item) => (
              <Link
                aria-current={section === item.key ? "page" : undefined}
                className={cn(
                  "flex min-h-12 items-center justify-between gap-3 border-b-2 border-transparent px-3 text-xs hover:bg-surface min-[761px]:min-h-11 min-[761px]:rounded-sm min-[761px]:border-b-0 min-[761px]:border-l-2",
                  section === item.key
                    ? "border-primary bg-surface text-foreground"
                    : "text-muted",
                )}
                key={item.key}
                to={sectionLink(item.key)}
              >
                {item.label}
                {item.key === "moments" && (
                  <span className="text-accent-foreground">
                    {album.moments.length}
                  </span>
                )}
              </Link>
            ))}
          </nav>
          <div className="min-w-0 px-3 py-6 min-[761px]:px-6 min-[761px]:py-7">
            <div hidden={section !== "details"}>
              <TitleForm album={album} />
            </div>
            {section === "moments" && <Moments moments={album.moments} />}
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
    <section aria-labelledby="album-details-heading" className="max-w-180">
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
            <h3
              className="mb-2 text-xs font-medium"
              id="source-description-label"
            >
              Description from Immich
            </h3>
            <p
              aria-labelledby="source-description-label"
              className="min-h-24 rounded-md border border-border bg-surface px-3 py-3 text-sm whitespace-pre-wrap"
            >
              {album.description || "No description in Immich."}
            </p>
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
