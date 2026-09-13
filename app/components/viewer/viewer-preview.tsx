import { useId } from "react";
import { useSearchParams } from "react-router-dom";

import type { ViewerTab } from "../../hooks/queries/viewer";
import type { AlbumDetail } from "../../types/generated/publishing";
import { PersonAvatar } from "../albums/person-avatar";
import { PageTitle } from "../shell/page-title";
import { usePreviewMode } from "../shell/preview-mode";
import { Combobox } from "../ui/combobox";
import { ViewerGallery } from "./viewer-gallery";

// The approved viewer presentation inside the editor, seen as one chosen
// person. The identity strip stays above the gallery the whole time.
export function ViewerPreview({ album }: { album: AlbumDetail }) {
  const [search, setSearch] = useSearchParams();
  const people = album.access.filter((person) => !person.deactivated);
  const person =
    people.find((candidate) => candidate.person_id === search.get("person")) ??
    people[0];
  const tab = search.get("tab") === "videos" ? "videos" : "photos";
  const labelId = useId();
  usePreviewMode();
  function tabLink(value: ViewerTab) {
    const next = new URLSearchParams(search);
    next.set("tab", value);
    return `?${next}`;
  }
  return (
    <section aria-labelledby="viewer-preview-heading">
      <h2 className="sr-only" id="viewer-preview-heading">
        Viewer preview
      </h2>
      {!person && <PageTitle title="Viewer preview" />}
      <div
        aria-label="Preview identity"
        className="flex flex-wrap items-center gap-x-5 gap-y-3 rounded-md bg-surface px-4 py-3 text-xs"
        role="region"
      >
        <span className="flex items-center gap-3">
          <span className="font-medium" id={labelId}>
            Preview as
          </span>
          <Combobox
            aria-labelledby={labelId}
            className="min-h-9 w-40"
            onChange={(id) => {
              const next = new URLSearchParams(search);
              next.set("person", id);
              setSearch(next);
            }}
            options={people.map((item) => ({
              value: item.person_id,
              label: item.display_name,
              leading: <PersonAvatar className="size-6" person={item} />,
            }))}
            placeholder="Choose a person"
            searchPlaceholder="Search people…"
            value={person?.person_id ?? ""}
          />
        </span>
        <p className="text-muted">
          Read only. Downloads and account actions are off.
          {!album.published && " Showing the view after publication."}
        </p>
      </div>
      {person ? (
        <div className="mt-8">
          <ViewerGallery
            context={{ albumID: album.id, personID: person.person_id }}
            key={`${album.id}:${person.person_id}`}
            personName={person.display_name}
            tab={tab}
            tabLinks={{ photos: tabLink("photos"), videos: tabLink("videos") }}
          />
        </div>
      ) : (
        <p className="mt-8 text-sm text-muted">
          No active viewers to preview yet. Add people first.
        </p>
      )}
    </section>
  );
}
