import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { SourceDraftBuilder } from "./SourceDrafting";
import {
  useDiscoverSources,
  useReconcileSource,
  useSources,
  useTriageSource,
} from "./hooks/queries/sources";
import { formatSourceDate } from "./format";
import { ErrorMessage } from "./presentation";
import type { Album } from "./types/generated/sources";
import type { SessionResponse } from "./types/generated/setup";

function SourceAlbumCard({
  album,
  csrfToken,
  onTriaged,
  onSelected,
  selected,
}: {
  album: Album;
  csrfToken: string;
  onTriaged: (message: string) => void;
  onSelected: (selected: boolean) => void;
  selected: boolean;
}) {
  const [inspecting, setInspecting] = useState(false);
  const triageMutation = useTriageSource(csrfToken, album, () => {
    onTriaged(
      album.disposition === "ignored"
        ? `Restored ${album.name} to the Source album inbox.`
        : `Ignored ${album.name}.`,
    );
  });
  const reconciliation = useReconcileSource(csrfToken, album.id, () => {
    onTriaged(`Queued reconciliation for ${album.name}.`);
  });
  const detailsID = `source-album-${album.id}-details`;
  return (
    <article className="source-album">
      <div className="source-album-summary">
        <div className="source-album-identity">
          {album.disposition !== "ignored" && !album.source_missing ? (
            <label>
              <input
                aria-label={`Select ${album.name} for drafting`}
                checked={selected}
                onChange={(event) => onSelected(event.target.checked)}
                type="checkbox"
              />
              <span>Select for drafting</span>
            </label>
          ) : null}
          <h3>{album.name}</h3>
          <p>
            {album.asset_count} {album.asset_count === 1 ? "item" : "items"}
            {album.source_missing ? " · Source missing" : ""}
          </p>
        </div>
        <button
          aria-controls={detailsID}
          aria-expanded={inspecting}
          aria-label={`${inspecting ? "Close details for" : "Inspect"} ${album.name}`}
          onClick={() => setInspecting((value) => !value)}
          type="button"
        >
          {inspecting ? "Close" : "Inspect"}
        </button>
      </div>
      {inspecting ? (
        <div className="source-details" id={detailsID}>
          <p>{album.description || "No source description."}</p>
          <dl>
            <div>
              <dt>Source updated</dt>
              <dd>{formatSourceDate(album.source_updated_at)}</dd>
            </div>
            <div>
              <dt>Last seen</dt>
              <dd>{formatSourceDate(album.last_seen_at)}</dd>
            </div>
          </dl>
          <ErrorMessage error={reconciliation.error ?? triageMutation.error} />
          <div className="source-details-actions">
            <button
              className="source-reconcile-action"
              disabled={reconciliation.isPending || triageMutation.isPending}
              onClick={() => reconciliation.mutate()}
              type="button"
            >
              {reconciliation.isPending ? "Queueing…" : "Reconcile now"}
            </button>
            {album.disposition !== "drafted" ? (
              <button
                className="source-primary-action"
                disabled={triageMutation.isPending || reconciliation.isPending}
                onClick={() => triageMutation.mutate()}
                type="button"
              >
                {triageMutation.isPending
                  ? "Saving…"
                  : album.disposition === "ignored"
                    ? "Restore to inbox"
                    : "Ignore Source album"}
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
    </article>
  );
}

export function SourceWorkspace({
  session,
  onSignOut,
  signOutError,
  signOutPending,
}: {
  session: SessionResponse;
  onSignOut: () => void;
  signOutError: Error | null;
  signOutPending: boolean;
}) {
  const [triageStatus, setTriageStatus] = useState("");
  const [selectedAlbumIDs, setSelectedAlbumIDs] = useState<Set<string>>(
    new Set(),
  );
  const [drafting, setDrafting] = useState(false);
  const [searchParams, setSearchParams] = useSearchParams();
  const sourceView = searchParams.get("source_view");
  const disposition =
    sourceView === "ignored"
      ? "ignored"
      : sourceView === "drafted"
        ? "drafted"
        : "unreviewed";
  const selectDisposition = (
    nextDisposition: "unreviewed" | "drafted" | "ignored",
  ) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (nextDisposition === "unreviewed") next.delete("source_view");
      else next.set("source_view", nextDisposition);
      return next;
    });
  };
  const unreviewedSources = useSources(session.csrf_token, "unreviewed");
  const draftedSources = useSources(session.csrf_token, "drafted");
  const ignoredSources = useSources(session.csrf_token, "ignored");
  const sources =
    disposition === "drafted"
      ? draftedSources
      : disposition === "ignored"
        ? ignoredSources
        : unreviewedSources;
  const albums = sources.data?.pages.flatMap((page) => page.albums);
  const selectableAlbums = useMemo(
    () =>
      [unreviewedSources.data, draftedSources.data].flatMap(
        (data) => data?.pages.flatMap((page) => page.albums) ?? [],
      ),
    [draftedSources.data, unreviewedSources.data],
  );
  const selectedAlbums = [...selectedAlbumIDs].flatMap((id) => {
    const album = selectableAlbums.find((candidate) => candidate.id === id);
    return album && !album.source_missing ? [album] : [];
  });
  const discover = useDiscoverSources(session.csrf_token);
  return (
    <section aria-labelledby="sources-title" className="source-workspace">
      <header className="source-header">
        <div>
          <p className="step-label">Curator workspace</p>
          <h2 id="sources-title">Source albums</h2>
          <p>
            Inspect owned Immich albums privately. Nothing here is visible to
            Recipients.
          </p>
        </div>
        <div className="source-header-actions">
          <button
            className="source-organize"
            onClick={() =>
              setSearchParams((current) => {
                const next = new URLSearchParams(current);
                next.set("workspace", "drafts");
                return next;
              })
            }
            type="button"
          >
            Organize existing drafts
          </button>
          <button
            className="source-connect"
            disabled={discover.isPending}
            onClick={() => discover.mutate()}
            type="button"
          >
            {discover.isPending ? "Validating…" : "Connect and discover"}
          </button>
          <button
            className="source-sign-out"
            disabled={signOutPending}
            onClick={onSignOut}
            type="button"
          >
            {signOutPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </header>
      {discover.data ? (
        <p aria-live="polite" className="source-success">
          Immich v3.0.3 connected. Found {discover.data.discovered_count} owned
          {discover.data.discovered_count === 1 ? " album" : " albums"}.
        </p>
      ) : null}
      <ErrorMessage error={discover.error} />
      <ErrorMessage error={signOutError} />
      <div aria-label="Source album views" className="source-tabs" role="group">
        <button
          aria-pressed={disposition === "unreviewed"}
          onClick={() => selectDisposition("unreviewed")}
          type="button"
        >
          Inbox
        </button>
        <button
          aria-pressed={disposition === "drafted"}
          onClick={() => selectDisposition("drafted")}
          type="button"
        >
          Drafted
        </button>
        <button
          aria-pressed={disposition === "ignored"}
          onClick={() => selectDisposition("ignored")}
          type="button"
        >
          Ignored
        </button>
      </div>
      <div className="source-selection-actions">
        <p aria-live="polite">
          {selectedAlbums.length} Source{" "}
          {selectedAlbums.length === 1 ? "album" : "albums"} selected
        </p>
        <button
          className="source-primary-action"
          disabled={selectedAlbums.length === 0}
          onClick={() => setDrafting(true)}
          type="button"
        >
          Draft selected Source albums
        </button>
        {selectedAlbums.length > 0 ? (
          <button
            onClick={() => {
              setSelectedAlbumIDs(new Set());
              setDrafting(false);
            }}
            type="button"
          >
            Clear selection
          </button>
        ) : null}
      </div>
      {drafting && selectedAlbums.length > 0 ? (
        <SourceDraftBuilder
          albums={selectedAlbums}
          csrfToken={session.csrf_token}
          onClose={() => setDrafting(false)}
        />
      ) : null}
      <p aria-live="polite" className="visually-hidden" role="status">
        {triageStatus}
      </p>
      {sources.isPending ? (
        <p className="source-empty">Loading Source albums…</p>
      ) : null}
      {sources.isError ? <ErrorMessage error={sources.error} /> : null}
      {albums?.length === 0 ? (
        <p className="source-empty">
          {disposition === "ignored"
            ? "No ignored Source albums."
            : disposition === "drafted"
              ? "No drafted Source albums yet."
              : "No unreviewed Source albums. Connect Immich to discover owned albums."}
        </p>
      ) : null}
      <div className="source-list">
        {albums?.map((album) => (
          <SourceAlbumCard
            album={album}
            csrfToken={session.csrf_token}
            key={album.id}
            onSelected={(selected) => {
              setSelectedAlbumIDs((current) => {
                const next = new Set(current);
                if (selected) next.add(album.id);
                else next.delete(album.id);
                return next;
              });
            }}
            onTriaged={(message) => {
              setTriageStatus(message);
              if (album.disposition !== "ignored") {
                setSelectedAlbumIDs((current) => {
                  const next = new Set(current);
                  next.delete(album.id);
                  return next;
                });
              }
            }}
            selected={selectedAlbumIDs.has(album.id)}
          />
        ))}
      </div>
      {sources.hasNextPage ? (
        <button
          className="source-load-more"
          disabled={sources.isFetchingNextPage}
          onClick={() => void sources.fetchNextPage()}
          type="button"
        >
          {sources.isFetchingNextPage ? "Loading…" : "Load more Source albums"}
        </button>
      ) : null}
    </section>
  );
}
