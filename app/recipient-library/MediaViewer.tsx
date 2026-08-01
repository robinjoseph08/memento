import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import type { SessionResponse } from "../types/generated/setup";
import { CommentThread } from "./CommentThread";
import { FavoriteControl } from "./FavoriteControl";
import { isUnavailableResponse, mediaMonthLabel } from "./mediaPresentation";
import type { RecipientMedia, RefreshedMediaAccess } from "./types";

type MediaAccess = RefreshedMediaAccess | "delivery-unavailable";

export function MediaViewer({
  media,
  session,
  refreshListingAccess,
  onClose,
  onOriginalDownload,
  onVideoStarted,
}: {
  media: RecipientMedia;
  session: SessionResponse;
  refreshListingAccess: () => Promise<RefreshedMediaAccess>;
  onClose: () => void;
  onOriginalDownload: () => void;
  onVideoStarted: () => void;
}) {
  const queryClient = useQueryClient();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const representationRetries = useRef(0);
  const videoStarted = useRef(false);
  const [mediaAccess, setMediaAccess] = useState<MediaAccess>(
    media.available ? "available" : "backing-unavailable",
  );
  const [representationAttempt, setRepresentationAttempt] = useState(0);
  const deliveryUnavailable = mediaAccess !== "available";
  const unavailableMedia = mediaAccess === "withdrawn";

  async function refreshMediaAccess(retryRepresentation = false) {
    let refreshedAccess: RefreshedMediaAccess = "access-unconfirmed";
    try {
      refreshedAccess = await refreshListingAccess();
    } catch {
      // Keep access unconfirmed when the listing itself cannot be refreshed.
    }
    if (
      retryRepresentation &&
      refreshedAccess === "available" &&
      representationRetries.current > 0
    ) {
      setMediaAccess("delivery-unavailable");
      return;
    }
    setMediaAccess(refreshedAccess);
    if (retryRepresentation && refreshedAccess === "available") {
      representationRetries.current++;
      setRepresentationAttempt((attempt) => attempt + 1);
    }
  }

  function classifyUnavailableMedia(error: unknown) {
    if (!isUnavailableResponse(error)) return;
    void refreshMediaAccess();
  }

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const viewerDialog: HTMLDialogElement = dialog;

    function containFocus(event: KeyboardEvent) {
      if (event.key !== "Tab") return;
      const focusable = viewerDialog.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), video[controls], [tabindex]:not([tabindex="-1"])',
      );
      const first = focusable.item(0);
      const last = focusable.item(focusable.length - 1);
      if (!first || !last) return;

      const active = document.activeElement;
      if (
        event.shiftKey &&
        (active === first || !viewerDialog.contains(active))
      ) {
        event.preventDefault();
        last.focus();
      } else if (
        !event.shiftKey &&
        (active === last || !viewerDialog.contains(active))
      ) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", containFocus, true);
    if (typeof viewerDialog.showModal === "function") {
      if (!viewerDialog.open) viewerDialog.showModal();
      return () => document.removeEventListener("keydown", containFocus, true);
    }

    if (!viewerDialog.open) viewerDialog.setAttribute("open", "");
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape")
        viewerDialog.dispatchEvent(new Event("close"));
    }
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("keydown", containFocus, true);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, []);

  function closeViewer() {
    const dialog = dialogRef.current;
    if (dialog && typeof dialog.close === "function") dialog.close();
    else onClose();
  }

  return (
    <dialog
      aria-label="Media viewer"
      aria-modal="true"
      className="media-viewer"
      onClose={onClose}
      ref={dialogRef}
    >
      <div className="viewer-media">
        {deliveryUnavailable ? (
          <span className="media-unavailable">
            {mediaAccess === "delivery-unavailable"
              ? "Media unavailable"
              : mediaAccess === "access-unconfirmed"
                ? "Access unconfirmed"
                : "Source unavailable"}
          </span>
        ) : media.media_type === "video" ? (
          <video
            aria-label="Video preview"
            controls
            key={`${media.id}-${representationAttempt}`}
            onError={() => void refreshMediaAccess(true)}
            onPlay={() => {
              if (videoStarted.current) return;
              videoStarted.current = true;
              onVideoStarted();
            }}
            playsInline
            poster={media.thumbnail_url}
            preload="metadata"
            src={media.video_url}
          />
        ) : (
          <img
            alt="Selected photo preview"
            key={`${media.id}-${representationAttempt}`}
            onError={() => void refreshMediaAccess(true)}
            src={media.preview_url}
          />
        )}
      </div>
      <aside className="viewer-details">
        <button autoFocus onClick={closeViewer} type="button">
          Close viewer
        </button>
        <h2>{media.media_type === "video" ? "Video" : "Photo"}</h2>
        <dl>
          <dt>Date</dt>
          <dd>{mediaMonthLabel(media)}</dd>
          <dt>Dimensions</dt>
          <dd>
            {media.width && media.height
              ? `${media.width} × ${media.height}`
              : "Unavailable"}
          </dd>
        </dl>
        {deliveryUnavailable ? (
          <div className="viewer-unavailable">
            <p className="form-error" role="alert">
              {mediaAccess === "backing-unavailable"
                ? "This Media's backing is temporarily unavailable. Its Library listing and interaction history remain available."
                : mediaAccess === "delivery-unavailable"
                  ? "This Media could not be loaded. Its Library listing and interaction history remain available."
                  : mediaAccess === "access-unconfirmed"
                    ? "This Media could not be loaded because Library access could not be refreshed. Try again from the Library."
                    : "This content is no longer available."}
            </p>
            <button
              onClick={() => {
                void queryClient.invalidateQueries({
                  queryKey: ["recipient-library"],
                });
                closeViewer();
              }}
              type="button"
            >
              Return to Library
            </button>
          </div>
        ) : null}
        {session.session_type === "public" && !deliveryUnavailable ? (
          <p className="viewer-download-warning">
            This original will remain on this public computer after sign-out.
          </p>
        ) : null}
        {deliveryUnavailable ? null : (
          <a
            className="viewer-download"
            download
            href={media.original_url}
            onClick={onOriginalDownload}
          >
            Download original
          </a>
        )}
        <FavoriteControl
          csrfToken={session.csrf_token}
          mediaID={media.id}
          onUnavailable={classifyUnavailableMedia}
          unavailableMedia={unavailableMedia}
        />
        <CommentThread
          csrfToken={session.csrf_token}
          mediaID={media.id}
          onUnavailable={classifyUnavailableMedia}
          unavailableMedia={unavailableMedia}
        />
      </aside>
    </dialog>
  );
}
