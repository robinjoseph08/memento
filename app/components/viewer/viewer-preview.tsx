import { Link, useSearchParams } from "react-router-dom";

import type { ViewerTab } from "../../hooks/queries/viewer";
import type { AccessPerson } from "../../types/generated/publishing";
import { PageTitle } from "../shell/page-title";
import { Combobox } from "../ui/combobox";
import { ViewerGallery } from "./viewer-gallery";

export function ViewerPreview({
  albumID,
  people,
}: {
  albumID: string;
  people: AccessPerson[];
}) {
  const [search, setSearch] = useSearchParams();
  const personID = search.get("person") ?? "";
  const person = people.find((candidate) => candidate.person_id === personID);
  const tab = search.get("tab") === "videos" ? "videos" : "photos";
  function tabLink(value: ViewerTab) {
    const next = new URLSearchParams(search);
    next.set("tab", value);
    return `?${next}`;
  }
  return (
    <div>
      {!personID && <PageTitle title="Viewer preview" />}
      <section
        aria-label="Preview identity"
        className="sticky top-0 z-10 mb-8 border-b border-border bg-background py-4"
      >
        <div className="flex flex-wrap items-center gap-4">
          <Combobox
            aria-label="Preview as person"
            className="w-64 max-w-full"
            onChange={(id) => {
              const next = new URLSearchParams(search);
              next.set("person", id);
              next.set("tab", tab);
              setSearch(next);
            }}
            options={people.map((item) => ({
              value: item.person_id,
              label: item.display_name,
            }))}
            placeholder="Choose a person"
            searchPlaceholder="Search people…"
            value={personID}
          />
          {personID && (
            <p className="text-sm font-medium">
              Previewing as {person?.display_name ?? "selected person"}
            </p>
          )}
        </div>
        <p className="mt-3 text-sm text-muted">
          Read-only preview of this person's view after publication. Downloads
          and account actions are disabled.
        </p>
        <Link
          className="-mx-2 mt-2 inline-flex rounded-md px-2 py-1 text-sm text-muted hover:bg-surface"
          to={`/curator/albums/${encodeURIComponent(albumID)}?section=access&pane=detail`}
        >
          Edit album access
        </Link>
      </section>
      {personID ? (
        <ViewerGallery
          context={{ albumID, personID }}
          key={`${albumID}:${personID}`}
          tab={tab}
          tabLinks={{ photos: tabLink("photos"), videos: tabLink("videos") }}
        />
      ) : (
        <p className="py-8 text-muted">
          Choose a person to see their album preview.
        </p>
      )}
    </div>
  );
}
