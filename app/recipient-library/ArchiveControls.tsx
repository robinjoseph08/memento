import { useEffect, useState, type ReactNode } from "react";

import { apiResponse } from "../api";
import { usePrepareRecipientArchive } from "../hooks/queries/archives";
import type { PlanResponse } from "../types/generated/archives";
import { LibraryError } from "./presentation";
import type { SubsetArchiveModel } from "./useSubsetArchiveModel";

function archivePartSize(size: number) {
  return `${new Intl.NumberFormat().format(size)} ${size === 1 ? "byte" : "bytes"}`;
}

export function ArchiveDownloads({
  csrfToken,
  plan,
  publicComputer,
}: {
  csrfToken: string;
  plan: PlanResponse;
  publicComputer: boolean;
}) {
  const [downloadingPart, setDownloadingPart] = useState<number>();
  const [downloadedParts, setDownloadedParts] = useState<Set<number>>(
    new Set(),
  );
  const [error, setError] = useState<Error | null>(null);
  const expiration = Date.parse(plan.expires_at);
  const [expired, setExpired] = useState(
    () => !Number.isFinite(expiration) || Date.now() >= expiration,
  );

  useEffect(() => {
    const remaining = expiration - Date.now();
    if (!Number.isFinite(expiration) || remaining <= 0) return;
    const timeout = window.setTimeout(() => setExpired(true), remaining);
    return () => window.clearTimeout(timeout);
  }, [expiration]);

  async function download(part: PlanResponse["parts"][number]) {
    if (expired) return;
    setDownloadingPart(part.part_number);
    setError(null);
    try {
      const response = await apiResponse(part.download_url, {
        method: "POST",
        headers: {
          Accept: "application/zip",
          "X-Memento-CSRF": csrfToken,
        },
      });
      const objectURL = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.download = part.filename;
      link.href = objectURL;
      link.style.display = "none";
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 0);
      setDownloadedParts((current) => new Set(current).add(part.part_number));
    } catch (cause) {
      setError(cause instanceof Error ? cause : new Error("Download failed."));
    } finally {
      setDownloadingPart(undefined);
    }
  }

  return (
    <section
      aria-label="Archive downloads"
      aria-live="polite"
      className="archive-downloads"
    >
      <strong>{plan.name}</strong>
      {expired ? (
        <span>Archive plan expired. Prepare a new archive to download it.</span>
      ) : (
        <span>
          {plan.item_count} {plan.item_count === 1 ? "item" : "items"}.
          Available until{" "}
          <time dateTime={plan.expires_at}>
            {new Date(expiration).toLocaleString()}
          </time>
          .
        </span>
      )}
      {publicComputer ? (
        <p>
          These archive files will remain on this public computer after
          sign-out.
        </p>
      ) : null}
      <div>
        {plan.parts.map((part) => {
          const downloaded = downloadedParts.has(part.part_number);
          const downloading = downloadingPart === part.part_number;
          const label =
            plan.parts.length === 1 ? "archive" : `part ${part.part_number}`;
          return (
            <div className="archive-part" key={part.part_number}>
              <span>
                {part.filename} ({archivePartSize(part.size)})
              </span>
              <button
                disabled={expired || downloaded || downloading}
                onClick={() => void download(part)}
                type="button"
              >
                {downloaded
                  ? `Downloaded ${label}`
                  : downloading
                    ? `Downloading ${label}…`
                    : `Download ${label}`}
              </button>
            </div>
          );
        })}
      </div>
      <LibraryError error={error} />
    </section>
  );
}

export function SubsetArchiveControls({
  csrfToken,
  enabled,
  model,
  onBegin,
  onCancel,
  publicComputer,
  selectedMedia,
}: {
  csrfToken: string;
  enabled: boolean;
  model: SubsetArchiveModel;
  onBegin: () => void;
  onCancel: () => void;
  publicComputer: boolean;
  selectedMedia: Set<string>;
}) {
  return (
    <>
      <div className="selection-toolbar">
        <button
          disabled={model.isPending}
          onClick={() => {
            if (enabled) {
              model.reset();
              onCancel();
            } else {
              onBegin();
            }
          }}
          type="button"
        >
          {enabled ? "Cancel selection" : "Select photos"}
        </button>
        {enabled ? (
          <button
            disabled={selectedMedia.size === 0 || model.isPending}
            onClick={() => model.prepare()}
            type="button"
          >
            {model.isPending
              ? "Preparing archive…"
              : `Prepare archive for ${selectedMedia.size} selected`}
          </button>
        ) : null}
      </div>
      {enabled && publicComputer ? (
        <p className="archive-warning">
          Subset archive files will remain on this public computer after
          sign-out.
        </p>
      ) : null}
      {enabled ? <LibraryError error={model.error} /> : null}
      {enabled && model.data ? (
        <ArchiveDownloads
          csrfToken={csrfToken}
          key={model.data.parts.map((part) => part.download_url).join("|")}
          plan={model.data}
          publicComputer={publicComputer}
        />
      ) : null}
    </>
  );
}

export function EventArchiveControls({
  csrfToken,
  eventID,
  heading,
  publicComputer,
  searchAction,
}: {
  csrfToken: string;
  eventID: string;
  heading: ReactNode;
  publicComputer: boolean;
  searchAction: ReactNode;
}) {
  const archive = usePrepareRecipientArchive(csrfToken);
  return (
    <>
      <header className="library-heading">
        {heading}
        <button
          disabled={archive.isPending}
          onClick={() =>
            archive.mutate({
              scope: "event",
              event_id: eventID,
              media_ids: [],
            })
          }
          type="button"
        >
          {archive.isPending ? "Preparing archive…" : "Prepare Event archive"}
        </button>
        {publicComputer ? (
          <p className="archive-warning">
            Event archive files will remain on this public computer after
            sign-out.
          </p>
        ) : null}
        {searchAction}
      </header>
      <LibraryError error={archive.error} />
      {archive.data ? (
        <ArchiveDownloads
          csrfToken={csrfToken}
          key={archive.data.parts.map((part) => part.download_url).join("|")}
          plan={archive.data}
          publicComputer={publicComputer}
        />
      ) : null}
    </>
  );
}
