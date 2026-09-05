import { useState } from "react";
import {
  Link,
  useLocation,
  useParams,
  useSearchParams,
} from "react-router-dom";

import { Icon } from "../viewer-prototype/artwork";
import { album, videos } from "../viewer-prototype/fixtures";
import {
  AlbumHeader,
  Lightbox,
  PhotoGallery,
  VideoGallery,
} from "../viewer-prototype/ViewerPrototype";
import {
  dateRange,
  people,
  studyPhotos,
  viewerCover,
  visibleEntries,
  type Study,
} from "./fixtures";

const base = "/prototype/curator/album";

export function AlbumDetails({
  study,
  onDirty,
  onSave,
}: {
  study: Study;
  onDirty: () => void;
  onSave: (study: Study, message: string) => void;
}) {
  const [error, setError] = useState("");
  return (
    <section className="curator-section">
      <h2>Album details</h2>
      <form
        className="curator-form"
        onChange={onDirty}
        onSubmit={(event) => {
          event.preventDefault();
          const input = event.currentTarget.elements.namedItem(
            "title",
          ) as HTMLInputElement;
          const title = input.value.trim();
          if (!title) {
            setError("Enter an album title.");
            input.focus();
            return;
          }
          onSave({ ...study, title }, "Album title saved.");
        }}
      >
        <label>
          Album title
          <input
            aria-describedby={error ? "title-error" : undefined}
            aria-invalid={!!error}
            defaultValue={study.title}
            name="title"
          />
        </label>
        {error && (
          <p id="title-error" role="alert">
            {error}
          </p>
        )}
        <label>
          Description from Immich
          <textarea readOnly rows={3} value={album.description} />
        </label>
        <p className="curator-hint">
          Edit the description in Immich, then review a manual sync. Sync is
          outside this study.
        </p>
        <button className="action-button" type="submit">
          Save Album details
        </button>
      </form>
      <section className="readiness">
        <h3>Before you publish</h3>
        <ul>
          <li>
            <Icon name="check" />
            Album has a title
          </li>
          <li>
            <Icon name="check" />
            {study.entries.length} items assigned to {study.moments.length}{" "}
            Moments
          </li>
          <li>
            <Icon name="check" />
            No unfinished structural edits
          </li>
        </ul>
        <p className="curator-hint">
          Review Moment access and preview as a person, then publish when you're
          ready. Recommendations and missing chapters never block publication.
        </p>
      </section>
    </section>
  );
}

export function ViewerPreview({ study }: { study: Study }) {
  const [params, setParams] = useSearchParams();
  const { tab = "photos", mediaId } = useParams();
  const location = useLocation();
  const person = people.includes(params.get("person") ?? "")
    ? params.get("person")!
    : "Alex";
  const items = visibleEntries(study, person);
  const cover = viewerCover(study, person);
  const photoItems = studyPhotos.filter((photo) =>
    items.some((item) => item.id === photo.id),
  );
  const videoItems = videos.filter((video) =>
    items.some((item) => item.id === video.id),
  );
  const nextTab = tab === "videos" ? "videos" : "photos";
  const count = nextTab === "photos" ? photoItems.length : videoItems.length;
  return (
    <div className="curator-viewer-preview">
      <section aria-label="Preview identity" className="preview-banner">
        <form onSubmit={(event) => event.preventDefault()}>
          <label>
            Preview as
            <select
              onChange={(event) =>
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set("person", event.target.value);
                  return next;
                })
              }
              value={person}
            >
              {people.map((name) => (
                <option key={name}>{name}</option>
              ))}
            </select>
          </label>
        </form>
        <p>
          Read only. Downloads and account actions are disabled.
          {!study.published && " Showing the view after publication."}
        </p>
      </section>
      {!items.length ? (
        <section className="empty-tab">
          <Icon name="album" />
          <h2>No album visible to {person}</h2>
          <p>No media is accessible. Recommendations do not grant access.</p>
        </section>
      ) : (
        <>
          <details className="preview-cover-note">
            <summary>Album list cover for {person}</summary>
            <div className="preview-card-study">
              {cover ? (
                <img alt={cover.label} src={cover.image} />
              ) : (
                <div className="neutral-cover">
                  <Icon name="album" />
                </div>
              )}
              <div>
                <h3>{study.title}</h3>
                <p>{dateRange(items)}</p>
                <p>
                  {photoItems.length} photos, {videoItems.length} videos
                </p>
                <p>
                  {cover
                    ? `Cover from ${study.moments.find((moment) => moment.id === cover.moment)?.title}.`
                    : "No configured Moment cover is accessible. The viewer gets a neutral placeholder."}
                </p>
              </div>
            </div>
          </details>
          <AlbumHeader
            cover={cover?.image ?? null}
            coverAlt={cover?.label ?? ""}
            dateRange={dateRange(items)}
            photoCount={photoItems.length}
            title={study.title}
            videoCount={videoItems.length}
          />
          <nav aria-label="Preview media" className="album-tabs-row">
            <div className="album-tabs">
              {["photos", "videos"].map((kind) => (
                <Link
                  aria-current={nextTab === kind ? "page" : undefined}
                  aria-selected={nextTab === kind}
                  key={kind}
                  to={`${base}/${kind}${location.search}`}
                >
                  <Icon name={kind === "photos" ? "photo" : "video"} />
                  {kind === "photos" ? "Photos" : "Videos"}
                  <span>
                    {kind === "photos" ? photoItems.length : videoItems.length}
                  </span>
                </Link>
              ))}
            </div>
          </nav>
          {!count ? (
            <section className="empty-tab">
              <h2>No {nextTab} in this album</h2>
              <p>
                You can see the album's{" "}
                {nextTab === "photos" ? "videos" : "photos"} in the other tab.
              </p>
              <Link
                className="action-button"
                to={`${base}/${nextTab === "photos" ? "videos" : "photos"}${location.search}`}
              >
                View {nextTab === "photos" ? "videos" : "photos"}
              </Link>
            </section>
          ) : nextTab === "photos" ? (
            <PhotoGallery
              basePath={base}
              items={photoItems}
              search={location.search}
            />
          ) : (
            <VideoGallery
              basePath={base}
              items={videoItems}
              search={location.search}
            />
          )}
          {mediaId && (
            <Lightbox
              basePath={base}
              mediaId={mediaId}
              photoItems={photoItems}
              previewName={person}
              search={location.search}
              tab={nextTab}
              title={study.title}
              videoItems={videoItems}
            />
          )}
        </>
      )}
    </div>
  );
}
