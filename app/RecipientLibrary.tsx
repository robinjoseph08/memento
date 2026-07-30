import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { APIError, apiJSON, apiNoContent, apiResponse } from "./api";
import { recordEngagement } from "./engagement";
import { ThemeToggle } from "./PWAControls";
import type {
  PlanRequest as ArchivePlanRequest,
  PlanResponse as ArchivePlanResponse,
} from "./types/generated/archives";
import type {
  Comment as MediaComment,
  ListResponse as CommentListResponse,
} from "./types/generated/comments";
import type { State as FavoriteState } from "./types/generated/favorites";
import type {
  Event as EventDetail,
  EventPage,
  EventSummary,
  Media,
  MediaPage,
  NewForYouResponse,
} from "./types/generated/library";
import type {
  Request as SearchRequest,
  Response as SearchResponse,
} from "./types/generated/search";
import type { SessionResponse } from "./types/generated/setup";

type Destination = "photos" | "events" | "favorites" | "search";
type SearchDateKind = "" | "year" | "month" | "date" | "range";
type SearchDateFilter = NonNullable<SearchRequest["date"]>;
type OpenedEvent = Pick<EventSummary, "id" | "title" | "publication_id">;

const libraryDestinations: ReadonlyArray<{
  destination: Destination;
  label: string;
}> = [
  { destination: "photos", label: "Photos" },
  { destination: "events", label: "Events" },
  { destination: "favorites", label: "Favorites" },
  { destination: "search", label: "Search" },
];

function LibraryNavigation({
  className,
  current,
  showBrand = false,
  showSearch = true,
  onNavigate,
}: {
  className: string;
  current?: Destination;
  showBrand?: boolean;
  showSearch?: boolean;
  onNavigate: (destination: Destination) => void;
}) {
  const destinations = showSearch
    ? libraryDestinations
    : libraryDestinations.filter((item) => item.destination !== "search");
  return (
    <nav aria-label="Library navigation" className={className}>
      {showBrand ? <div className="library-brand">Memento</div> : null}
      {destinations.map((item) => (
        <button
          aria-current={current === item.destination ? "page" : undefined}
          key={item.destination}
          onClick={() => onNavigate(item.destination)}
          type="button"
        >
          {item.label}
        </button>
      ))}
    </nav>
  );
}

function searchDateKind(value: string): SearchDateKind {
  return value === "year" ||
    value === "month" ||
    value === "date" ||
    value === "range"
    ? value
    : "";
}

function mediaLabel(media: Media) {
  if (!media.local_date_time) return "Date unavailable";
  const parsed = new Date(media.local_date_time);
  if (Number.isNaN(parsed.valueOf())) return "Date unavailable";
  return new Intl.DateTimeFormat(undefined, {
    month: "long",
    year: "numeric",
  }).format(parsed);
}

function mediaAlt(item: Media, index: number) {
  const kind = item.media_type === "video" ? "Video" : "Photo";
  const date = mediaLabel(item);
  return date === "Date unavailable"
    ? `${kind} ${index + 1}, date unavailable`
    : `${kind} ${index + 1} from ${date}`;
}

function isUnavailableResponse(error: unknown) {
  return error instanceof APIError && error.status === 404;
}

function Gallery({
  media,
  onOpen,
  selected,
  selectionEnabled = false,
  onToggle,
}: {
  media: Media[];
  onOpen: (media: Media) => void;
  selected?: Set<string>;
  selectionEnabled?: boolean;
  onToggle?: (media: Media) => void;
}) {
  return (
    <div aria-label="Media gallery" className="justified-gallery">
      {media.map((item, index) => (
        <figure
          className="gallery-item"
          key={item.id}
          style={{
            aspectRatio:
              item.width && item.height
                ? `${item.width} / ${item.height}`
                : "1",
            flexGrow: item.width && item.height ? item.width / item.height : 1,
          }}
        >
          <button
            aria-label={`Open ${mediaAlt(item, index)}`}
            className="viewer-trigger"
            onClick={() => onOpen(item)}
            type="button"
          >
            {item.available ? (
              <img
                alt={mediaAlt(item, index)}
                loading="lazy"
                src={item.thumbnail_url}
              />
            ) : (
              <span className="media-unavailable">Source unavailable</span>
            )}
          </button>
          {selectionEnabled && item.available ? (
            <label className="media-selection">
              <input
                aria-label={`${selected?.has(item.id) ? "Remove" : "Select"} ${mediaAlt(item, index)}`}
                checked={selected?.has(item.id) ?? false}
                onChange={() => onToggle?.(item)}
                type="checkbox"
              />
            </label>
          ) : null}
          {item.media_type === "video" ? (
            <span className="media-kind">Video</span>
          ) : null}
        </figure>
      ))}
    </div>
  );
}

function EventCards({
  events,
  onOpen,
}: {
  events: EventSummary[];
  onOpen: (event: EventSummary) => void;
}) {
  return (
    <div aria-label="Event gallery" className="event-gallery">
      {events.map((event) => {
        const ratio =
          event.cover_width && event.cover_height
            ? event.cover_width / event.cover_height
            : 1;
        return (
          <button
            className="event-card"
            key={event.id}
            onClick={() => onOpen(event)}
            style={{ flexBasis: `${ratio * 11}rem`, flexGrow: ratio }}
            type="button"
          >
            <span
              className="event-cover"
              style={{
                aspectRatio:
                  event.cover_width && event.cover_height
                    ? `${event.cover_width} / ${event.cover_height}`
                    : "1",
              }}
            >
              {event.cover_available ? (
                <img alt="" loading="lazy" src={event.thumbnail_url} />
              ) : (
                <span className="media-unavailable">Source unavailable</span>
              )}
            </span>
            <strong>{event.title}</strong>
            <span>
              {event.media_count} {event.media_count === 1 ? "item" : "items"}
            </span>
          </button>
        );
      })}
    </div>
  );
}

type RefreshedMediaAccess =
  "available" | "backing-unavailable" | "withdrawn" | "access-unconfirmed";
type MediaAccess = RefreshedMediaAccess | "delivery-unavailable";

function MediaViewer({
  media,
  session,
  refreshListingAccess,
  onClose,
  onOriginalDownload,
  onVideoStarted,
}: {
  media: Media;
  session: SessionResponse;
  refreshListingAccess: () => Promise<RefreshedMediaAccess>;
  onClose: () => void;
  onOriginalDownload: () => void;
  onVideoStarted: () => void;
}) {
  const queryClient = useQueryClient();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const commentRetry = useRef<{ body: string; key: string } | undefined>(
    undefined,
  );
  const representationRetries = useRef(0);
  const videoStarted = useRef(false);
  const [commentBody, setCommentBody] = useState("");
  const [mediaAccess, setMediaAccess] = useState<MediaAccess>(
    media.available ? "available" : "backing-unavailable",
  );
  const [representationAttempt, setRepresentationAttempt] = useState(0);
  const favorite = useQuery({
    queryKey: ["favorite", session.csrf_token, media.id],
    queryFn: async () => {
      try {
        return await apiJSON<FavoriteState>(`/api/favorites/${media.id}`);
      } catch (error) {
        void classifyUnavailableMedia(error);
        throw error;
      }
    },
    retry: false,
  });
  const comments = useInfiniteQuery({
    queryKey: ["comments", session.csrf_token, media.id],
    queryFn: async ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      try {
        return await apiJSON<CommentListResponse>(
          `/api/comments/media/${media.id}?${params.toString()}`,
        );
      } catch (error) {
        void classifyUnavailableMedia(error);
        throw error;
      }
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
  const commentItems =
    comments.data?.pages.flatMap((page) => page.comments) ?? [];
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

  async function classifyUnavailableMedia(error: unknown) {
    if (!isUnavailableResponse(error)) return;
    await refreshMediaAccess();
  }

  async function verifyMediaAfterUnavailableComment(error: unknown) {
    if (!isUnavailableResponse(error)) return;
    try {
      const state = await apiJSON<FavoriteState>(`/api/favorites/${media.id}`);
      queryClient.setQueryData(
        ["favorite", session.csrf_token, media.id],
        state,
      );
      await queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      });
    } catch (recheckError) {
      await classifyUnavailableMedia(recheckError);
    }
  }

  const toggleFavorite = useMutation({
    mutationFn: (next: boolean) =>
      apiJSON<FavoriteState>(`/api/favorites/${media.id}`, {
        method: next ? "PUT" : "DELETE",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: async (state) => {
      queryClient.setQueryData(
        ["favorite", session.csrf_token, media.id],
        state,
      );
      await queryClient.invalidateQueries({ queryKey: ["recipient-library"] });
    },
    onError: (error) => void classifyUnavailableMedia(error),
  });
  const createComment = useMutation({
    mutationFn: () => {
      const submission =
        commentRetry.current?.body === commentBody
          ? commentRetry.current
          : { body: commentBody, key: crypto.randomUUID() };
      commentRetry.current = submission;
      return apiJSON<MediaComment>(`/api/comments/media/${media.id}`, {
        method: "POST",
        headers: {
          "Idempotency-Key": submission.key,
          "X-Memento-CSRF": session.csrf_token,
        },
        body: JSON.stringify({ body: submission.body }),
      });
    },
    onSuccess: async () => {
      commentRetry.current = undefined;
      setCommentBody("");
      await queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      });
    },
    onError: (error) => void classifyUnavailableMedia(error),
  });
  const editComment = useMutation({
    mutationFn: ({
      id,
      body,
      version,
    }: {
      id: string;
      body: string;
      version: number;
    }) =>
      apiJSON<MediaComment>(`/api/comments/${id}`, {
        method: "PATCH",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": session.csrf_token,
        },
        body: JSON.stringify({ body }),
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      }),
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const deleteComment = useMutation({
    mutationFn: ({ id, version }: { id: string; version: number }) =>
      apiNoContent(`/api/comments/${id}`, {
        method: "DELETE",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": session.csrf_token,
        },
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      }),
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const moderateComment = useMutation({
    mutationFn: ({
      id,
      reason,
      version,
    }: {
      id: string;
      reason: string;
      version: number;
    }) =>
      apiNoContent(`/api/comments/${id}/moderate`, {
        method: "POST",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": session.csrf_token,
        },
        body: JSON.stringify({ reason }),
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      }),
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const muteComments = useMutation({
    mutationFn: (muted: boolean) =>
      apiNoContent(`/api/comments/media/${media.id}/mute`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ muted }),
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["comments", session.csrf_token, media.id],
      }),
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const viewerDialog: HTMLDialogElement = dialog;

    function containFocus(event: KeyboardEvent) {
      if (event.key !== "Tab") return;
      const focusable = viewerDialog.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), video[controls], [tabindex]:not([tabindex="-1"])',
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
      if (event.key === "Escape") {
        viewerDialog.dispatchEvent(new Event("close"));
      }
    }
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("keydown", containFocus, true);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, []);

  function closeViewer() {
    const dialog = dialogRef.current;
    if (dialog && typeof dialog.close === "function") {
      dialog.close();
    } else {
      onClose();
    }
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
          <dd>{mediaLabel(media)}</dd>
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
        <section aria-labelledby="favorite-title" className="viewer-favorite">
          <h3 id="favorite-title">Favorite</h3>
          <button
            aria-pressed={favorite.data?.favorite ?? false}
            disabled={
              unavailableMedia || favorite.isPending || toggleFavorite.isPending
            }
            onClick={() => toggleFavorite.mutate(!favorite.data?.favorite)}
            type="button"
          >
            {favorite.data?.favorite ? "Remove Favorite" : "Add Favorite"}
          </button>
          <p>Favorites aren&apos;t shared with other recipients.</p>
          <LibraryError
            error={
              unavailableMedia ||
              isUnavailableResponse(favorite.error ?? toggleFavorite.error)
                ? null
                : (favorite.error ?? toggleFavorite.error)
            }
          />
        </section>
        <section aria-labelledby="comments-title" className="viewer-comments">
          <div className="viewer-comments-heading">
            <h3 id="comments-title">Comments</h3>
            <label>
              <input
                checked={comments.data?.pages[0]?.muted ?? false}
                disabled={
                  unavailableMedia ||
                  muteComments.isPending ||
                  !(comments.data?.pages[0]?.can_mute ?? false)
                }
                onChange={(event) => muteComments.mutate(event.target.checked)}
                type="checkbox"
              />
              Mute future Comment notifications
            </label>
          </div>
          <LibraryError
            error={
              unavailableMedia ||
              isUnavailableResponse(
                comments.error ??
                  createComment.error ??
                  editComment.error ??
                  deleteComment.error ??
                  moderateComment.error ??
                  muteComments.error,
              )
                ? null
                : (comments.error ??
                  createComment.error ??
                  editComment.error ??
                  deleteComment.error ??
                  moderateComment.error ??
                  muteComments.error)
            }
          />
          <ol className="comment-list">
            {commentItems.map((comment) => (
              <li key={comment.id}>
                <div>
                  <strong>{comment.author_name}</strong>
                  <time dateTime={comment.created_at}>
                    {new Intl.DateTimeFormat(undefined, {
                      dateStyle: "medium",
                      timeStyle: "short",
                    }).format(new Date(comment.created_at))}
                  </time>
                </div>
                {comment.state === "deleted" ? (
                  <p className="comment-tombstone">Comment deleted.</p>
                ) : comment.state === "moderated" ? (
                  <p className="comment-tombstone">
                    Comment moderated by{" "}
                    {comment.moderator_name ?? "the Curator"}.
                  </p>
                ) : (
                  <p>{comment.body}</p>
                )}
                <div className="comment-actions">
                  {comment.can_edit ? (
                    <button
                      disabled={unavailableMedia || editComment.isPending}
                      onClick={() => {
                        const body = window.prompt(
                          "Edit Comment",
                          comment.body,
                        );
                        if (body?.trim())
                          editComment.mutate({
                            id: comment.id,
                            body,
                            version: comment.version,
                          });
                      }}
                      type="button"
                    >
                      Edit
                    </button>
                  ) : null}
                  {comment.can_delete ? (
                    <button
                      disabled={unavailableMedia || deleteComment.isPending}
                      onClick={() => {
                        if (
                          window.confirm(
                            "Delete this Comment? This cannot be undone.",
                          )
                        ) {
                          deleteComment.mutate({
                            id: comment.id,
                            version: comment.version,
                          });
                        }
                      }}
                      type="button"
                    >
                      Delete
                    </button>
                  ) : null}
                  {comment.can_moderate ? (
                    <button
                      disabled={unavailableMedia || moderateComment.isPending}
                      onClick={() => {
                        const reason = window.prompt("Moderation reason");
                        if (reason?.trim())
                          moderateComment.mutate({
                            id: comment.id,
                            reason,
                            version: comment.version,
                          });
                      }}
                      type="button"
                    >
                      Moderate
                    </button>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>
          {comments.hasNextPage ? (
            <button
              disabled={unavailableMedia || comments.isFetchingNextPage}
              onClick={() => void comments.fetchNextPage()}
              type="button"
            >
              {comments.isFetchingNextPage ? "Loading…" : "Load more Comments"}
            </button>
          ) : null}
          <form
            className="comment-form"
            onSubmit={(event: FormEvent<HTMLFormElement>) => {
              event.preventDefault();
              createComment.mutate();
            }}
          >
            <label>
              Add a Comment
              <textarea
                disabled={unavailableMedia}
                maxLength={2000}
                onChange={(event) => {
                  const body = event.target.value;
                  if (commentRetry.current?.body !== body)
                    commentRetry.current = undefined;
                  setCommentBody(body);
                }}
                required
                value={commentBody}
              />
            </label>
            <button
              disabled={
                unavailableMedia ||
                createComment.isPending ||
                !commentBody.trim()
              }
              type="submit"
            >
              {createComment.isPending ? "Posting…" : "Post Comment"}
            </button>
          </form>
        </section>
      </aside>
    </dialog>
  );
}

function archivePartSize(size: number) {
  return `${new Intl.NumberFormat().format(size)} ${size === 1 ? "byte" : "bytes"}`;
}

export function ArchiveDownloads({
  csrfToken,
  plan,
  publicComputer,
}: {
  csrfToken: string;
  plan: ArchivePlanResponse;
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

  async function download(part: ArchivePlanResponse["parts"][number]) {
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
    <section aria-label="Archive downloads" className="archive-downloads">
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

function LibraryError({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

export function RecipientLibrary({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [destination, setDestination] = useState<Destination>("photos");
  const [openedEvent, setOpenedEvent] = useState<OpenedEvent>();
  const [openedMedia, setOpenedMedia] = useState<Media>();
  const [selectionEnabled, setSelectionEnabled] = useState(false);
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [archivePlan, setArchivePlan] = useState<ArchivePlanResponse>();
  const [searchText, setSearchText] = useState("");
  const [dateKind, setDateKind] = useState<SearchDateKind>("");
  const [searchYear, setSearchYear] = useState("");
  const [searchMonth, setSearchMonth] = useState("");
  const [searchDate, setSearchDate] = useState("");
  const [searchStart, setSearchStart] = useState("");
  const [searchEnd, setSearchEnd] = useState("");
  const mediaOpener = useRef<HTMLElement | null>(null);
  const endpoint = destination === "favorites" ? "favorites" : "photos";
  const photos = useInfiniteQuery({
    queryKey: ["recipient-library", session.csrf_token, endpoint],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<MediaPage>(`/api/me/${endpoint}?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled:
      destination !== "events" && destination !== "search" && !openedEvent,
    retry: false,
  });
  const events = useInfiniteQuery({
    queryKey: ["recipient-events", session.csrf_token],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "24" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventPage>(`/api/me/events?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: destination === "events" && !openedEvent,
    retry: false,
  });
  const newForYou = useQuery({
    queryKey: ["new-for-you", session.csrf_token],
    queryFn: () => apiJSON<NewForYouResponse>("/api/me/new-for-you"),
    enabled: destination === "photos" && !openedEvent,
    retry: false,
  });
  const event = useInfiniteQuery({
    queryKey: ["recipient-event", session.csrf_token, openedEvent?.id],
    queryFn: ({ pageParam }) => {
      if (!openedEvent) throw new Error("Choose an Event first.");
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventDetail>(
        `/api/me/events/${openedEvent.id}?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: Boolean(openedEvent),
    retry: false,
  });
  const archive = useMutation({
    mutationFn: (request: ArchivePlanRequest) =>
      apiJSON<ArchivePlanResponse>("/api/me/archives", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: (plan) => setArchivePlan(plan),
  });
  const search = useMutation({
    mutationFn: (request: SearchRequest) =>
      apiJSON<SearchResponse>("/api/search", {
        method: "POST",
        body: JSON.stringify(request),
      }),
  });
  const seen = useMutation({
    mutationFn: (publicationID: string) =>
      apiNoContent(`/api/me/new-for-you/${publicationID}/seen`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["new-for-you", session.csrf_token],
      }),
  });
  const media = useMemo(
    () => photos.data?.pages.flatMap((page) => page.media) ?? [],
    [photos.data],
  );
  const eventItems = events.data?.pages.flatMap((page) => page.events) ?? [];
  const eventMedia = event.data?.pages.flatMap((page) => page.media) ?? [];
  const dates = useMemo(() => [...new Set(media.map(mediaLabel))], [media]);

  useEffect(() => {
    const recordVisit = () => {
      if (document.visibilityState === "visible") {
        void recordEngagement(session, { kind: "visit" });
      }
    };
    recordVisit();
    document.addEventListener("visibilitychange", recordVisit);
    return () => document.removeEventListener("visibilitychange", recordVisit);
  }, [session]);

  function openEvent(summary: EventSummary, isNew = false) {
    setArchivePlan(undefined);
    setOpenedEvent(summary);
    void recordEngagement(session, {
      kind: "event_opened",
      event_id: summary.id,
    });
    if (isNew) seen.mutate(summary.publication_id);
  }

  function toggleMedia(item: Media) {
    setArchivePlan(undefined);
    setSelectedMedia((current) => {
      const next = new Set(current);
      if (next.has(item.id)) next.delete(item.id);
      else next.add(item.id);
      return next;
    });
  }

  function planSubset() {
    archive.mutate({
      scope: "subset",
      event_id: null,
      media_ids: [...selectedMedia],
    });
  }

  function planEvent() {
    if (!openedEvent) return;
    archive.mutate({
      scope: "event",
      event_id: openedEvent.id,
      media_ids: [],
    });
  }

  function navigateTo(destination: Destination) {
    setArchivePlan(undefined);
    setSelectionEnabled(false);
    setSelectedMedia(new Set());
    setOpenedEvent(undefined);
    setDestination(destination);
    void recordEngagement(session, {
      kind: "destination_opened",
      destination,
    });
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    let date: SearchDateFilter | null = null;
    if (dateKind === "year") {
      date = { kind: "year", year: Number(searchYear) };
    } else if (dateKind === "month") {
      date = { kind: "month", month: searchMonth };
    } else if (dateKind === "date") {
      date = { kind: "date", date: searchDate };
    } else if (dateKind === "range") {
      date = {
        kind: "range",
        start_date: searchStart,
        end_date: searchEnd,
      };
    }
    search.mutate({ query: searchText, date });
  }

  function classifyRefreshedListing(current: Media | undefined) {
    if (!current) return "withdrawn" as const;
    return current.available
      ? ("available" as const)
      : ("backing-unavailable" as const);
  }

  async function refreshListingAccess(): Promise<RefreshedMediaAccess> {
    if (openedEvent) {
      const refreshed = await event.refetch();
      if (refreshed.error) return "access-unconfirmed";
      const current = refreshed.data?.pages
        .flatMap((page) => page.media)
        .find((item) => item.id === openedMedia?.id);
      return classifyRefreshedListing(current);
    }
    if (destination === "photos" || destination === "favorites") {
      const refreshed = await photos.refetch();
      if (refreshed.error) return "access-unconfirmed";
      const current = refreshed.data?.pages
        .flatMap((page) => page.media)
        .find((item) => item.id === openedMedia?.id);
      return classifyRefreshedListing(current);
    }
    if (destination === "search" && search.variables) {
      try {
        const refreshed = await search.mutateAsync(search.variables);
        const current = refreshed.photos.find(
          (item) => item.id === openedMedia?.id,
        );
        return classifyRefreshedListing(current);
      } catch {
        return "access-unconfirmed";
      }
    }
    return "withdrawn";
  }

  function openMedia(item: Media) {
    mediaOpener.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setOpenedMedia(item);
    void recordEngagement(session, {
      kind: "media_opened",
      media_item_id: item.id,
    });
  }

  function closeMedia() {
    const opener = mediaOpener.current;
    setOpenedMedia(undefined);
    requestAnimationFrame(() => {
      if (opener?.isConnected) opener.focus();
    });
  }

  return (
    <section aria-label="Recipient library" className="recipient-library">
      <LibraryNavigation
        className="library-rail"
        current={openedEvent ? undefined : destination}
        onNavigate={navigateTo}
        showBrand
        showSearch={false}
      />
      <div className="library-content">
        <div className="app-preferences">
          <ThemeToggle />
        </div>
        {openedEvent ? (
          <>
            <button
              className="library-back"
              onClick={() => {
                setArchivePlan(undefined);
                setOpenedEvent(undefined);
              }}
              type="button"
            >
              Back to{" "}
              {destination === "photos"
                ? "Photos"
                : destination === "search"
                  ? "Search"
                  : "Events"}
            </button>
            <header className="library-heading">
              <h1>{event.data?.pages[0]?.title ?? openedEvent.title}</h1>
              {event.data?.pages[0]?.description ? (
                <p>{event.data.pages[0].description}</p>
              ) : null}
              {event.data?.pages[0] ? (
                <span>
                  {event.data.pages[0].media_count}{" "}
                  {event.data.pages[0].media_count === 1 ? "item" : "items"}
                </span>
              ) : null}
              <button
                disabled={archive.isPending}
                onClick={planEvent}
                type="button"
              >
                {archive.isPending
                  ? "Preparing archive…"
                  : "Prepare Event archive"}
              </button>
              {session.session_type === "public" ? (
                <p className="archive-warning">
                  Event archive files will remain on this public computer after
                  sign-out.
                </p>
              ) : null}
              <button
                aria-label="Search library"
                className="desktop-search-action"
                onClick={() => navigateTo("search")}
                type="button"
              >
                Search
              </button>
            </header>
            <LibraryError error={event.error ?? archive.error} />
            {archivePlan ? (
              <ArchiveDownloads
                csrfToken={session.csrf_token}
                key={archivePlan.parts
                  .map((part) => part.download_url)
                  .join("|")}
                plan={archivePlan}
                publicComputer={session.session_type === "public"}
              />
            ) : null}
            <Gallery media={eventMedia} onOpen={openMedia} />
            {event.hasNextPage ? (
              <button
                disabled={event.isFetchingNextPage}
                onClick={() => void event.fetchNextPage()}
                type="button"
              >
                {event.isFetchingNextPage ? "Loading…" : "Load more"}
              </button>
            ) : null}
          </>
        ) : (
          <>
            <header className="library-heading">
              <p className="step-label">Private family archive</p>
              <h1>{destination[0].toUpperCase() + destination.slice(1)}</h1>
              {destination === "favorites" ? (
                <p>Favorites aren&apos;t shared with other recipients.</p>
              ) : null}
              <button
                aria-current={destination === "search" ? "page" : undefined}
                aria-label="Search library"
                className="desktop-search-action"
                onClick={() => navigateTo("search")}
                type="button"
              >
                Search
              </button>
            </header>
            {destination === "photos" ? (
              <LibraryError error={newForYou.error ?? seen.error} />
            ) : null}
            {destination === "photos" && newForYou.data?.events.length ? (
              <section
                aria-labelledby="new-for-you-title"
                className="new-for-you"
              >
                <h2 id="new-for-you-title">New for you</h2>
                <EventCards
                  events={newForYou.data.events}
                  onOpen={(summary) => openEvent(summary, true)}
                />
              </section>
            ) : null}
            {destination === "search" ? (
              <div className="recipient-search">
                <form className="search-form" onSubmit={submitSearch}>
                  <label>
                    Search published Events, Place labels, and People
                    <input
                      autoComplete="off"
                      maxLength={200}
                      disabled={search.isPending}
                      onChange={(event) => {
                        search.reset();
                        setSearchText(event.target.value);
                      }}
                      placeholder="Family picnic"
                      type="search"
                      value={searchText}
                    />
                  </label>
                  <label>
                    Date filter
                    <select
                      disabled={search.isPending}
                      onChange={(event) => {
                        search.reset();
                        setDateKind(searchDateKind(event.target.value));
                      }}
                      value={dateKind}
                    >
                      <option value="">No date filter</option>
                      <option value="year">Year</option>
                      <option value="month">Month</option>
                      <option value="date">Exact date</option>
                      <option value="range">Date range</option>
                    </select>
                  </label>
                  {dateKind === "year" ? (
                    <label>
                      Year
                      <input
                        max={9999}
                        min={1}
                        disabled={search.isPending}
                        onChange={(event) => {
                          search.reset();
                          setSearchYear(event.target.value);
                        }}
                        required
                        type="number"
                        value={searchYear}
                      />
                    </label>
                  ) : null}
                  {dateKind === "month" ? (
                    <label>
                      Month
                      <input
                        disabled={search.isPending}
                        onChange={(event) => {
                          search.reset();
                          setSearchMonth(event.target.value);
                        }}
                        required
                        type="month"
                        value={searchMonth}
                      />
                    </label>
                  ) : null}
                  {dateKind === "date" ? (
                    <label>
                      Date
                      <input
                        disabled={search.isPending}
                        onChange={(event) => {
                          search.reset();
                          setSearchDate(event.target.value);
                        }}
                        required
                        type="date"
                        value={searchDate}
                      />
                    </label>
                  ) : null}
                  {dateKind === "range" ? (
                    <>
                      <label>
                        Start date
                        <input
                          disabled={search.isPending}
                          onChange={(event) => {
                            search.reset();
                            setSearchStart(event.target.value);
                          }}
                          required
                          type="date"
                          value={searchStart}
                        />
                      </label>
                      <label>
                        End date
                        <input
                          min={searchStart}
                          disabled={search.isPending}
                          onChange={(event) => {
                            search.reset();
                            setSearchEnd(event.target.value);
                          }}
                          required
                          type="date"
                          value={searchEnd}
                        />
                      </label>
                    </>
                  ) : null}
                  <button
                    aria-label="Run search"
                    disabled={
                      search.isPending || (!searchText.trim() && !dateKind)
                    }
                    type="submit"
                  >
                    {search.isPending ? "Searching…" : "Search"}
                  </button>
                </form>
                <LibraryError error={search.error} />
                {search.data ? (
                  <p aria-live="polite" className="search-summary">
                    {search.data.total_photos} matching{" "}
                    {search.data.total_photos === 1 ? "photo" : "photos"}.{" "}
                    {search.data.total_events} matching{" "}
                    {search.data.total_events === 1 ? "Event" : "Events"}.
                    {search.data.has_more
                      ? " Refine the search to see fewer results."
                      : ""}
                  </p>
                ) : null}
                {search.data?.people.length ? (
                  <section aria-labelledby="search-people-title">
                    <h2 id="search-people-title">People</h2>
                    <ul className="search-people">
                      {search.data.people.map((person) => (
                        <li key={`${person.person_id}-${person.event_id}`}>
                          <strong>{person.person_name}</strong> attended part of{" "}
                          {person.event_title}.
                        </li>
                      ))}
                    </ul>
                  </section>
                ) : null}
                {search.data?.events.length ? (
                  <section aria-labelledby="search-events-title">
                    <h2 id="search-events-title">Events</h2>
                    <div className="event-gallery">
                      {search.data.events.map((event) => {
                        const ratio =
                          event.cover_width && event.cover_height
                            ? event.cover_width / event.cover_height
                            : 1;
                        return (
                          <button
                            className="event-card"
                            key={event.id}
                            onClick={() =>
                              setOpenedEvent({
                                id: event.id,
                                title: event.title,
                                publication_id: "",
                              })
                            }
                            style={{
                              flexBasis: `${ratio * 11}rem`,
                              flexGrow: ratio,
                            }}
                            type="button"
                          >
                            <span
                              className="event-cover"
                              style={{
                                aspectRatio:
                                  event.cover_width && event.cover_height
                                    ? `${event.cover_width} / ${event.cover_height}`
                                    : "1",
                              }}
                            >
                              {event.cover_available ? (
                                <img
                                  alt=""
                                  loading="lazy"
                                  src={event.thumbnail_url}
                                />
                              ) : (
                                <span className="media-unavailable">
                                  Source unavailable
                                </span>
                              )}
                            </span>
                            <strong>{event.title}</strong>
                            <span>
                              {event.media_count} matching{" "}
                              {event.media_count === 1 ? "item" : "items"}
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  </section>
                ) : null}
                {search.data?.photos.length ? (
                  <section aria-labelledby="search-photos-title">
                    <h2 id="search-photos-title">Photos</h2>
                    <Gallery media={search.data.photos} onOpen={openMedia} />
                  </section>
                ) : null}
                {search.data &&
                search.data.total_events === 0 &&
                search.data.total_photos === 0 ? (
                  <p className="library-empty">
                    Nothing in your shared collection matched.
                  </p>
                ) : null}
              </div>
            ) : destination === "events" ? (
              <>
                <LibraryError error={events.error} />
                <EventCards events={eventItems} onOpen={openEvent} />
                {!events.isPending &&
                !events.error &&
                eventItems.length === 0 ? (
                  <p className="library-empty">No Events are available.</p>
                ) : null}
                {events.hasNextPage ? (
                  <button
                    disabled={events.isFetchingNextPage}
                    onClick={() => void events.fetchNextPage()}
                    type="button"
                  >
                    {events.isFetchingNextPage
                      ? "Loading…"
                      : "Load more Events"}
                  </button>
                ) : null}
              </>
            ) : (
              <div className="photo-library-layout">
                {dates.length ? (
                  <>
                    <label className="mobile-date-nav">
                      Jump to date
                      <select
                        onChange={(change) =>
                          document
                            .getElementById(
                              `date-${change.target.selectedIndex}`,
                            )
                            ?.scrollIntoView({ behavior: "smooth" })
                        }
                      >
                        {dates.map((date) => (
                          <option key={date}>{date}</option>
                        ))}
                      </select>
                    </label>
                    <nav aria-label="Photo dates" className="date-rail">
                      {dates.map((date, index) => (
                        <a href={`#date-${index}`} key={date}>
                          {date}
                        </a>
                      ))}
                    </nav>
                  </>
                ) : null}
                <div className="dated-galleries">
                  <LibraryError error={photos.error ?? archive.error} />
                  {destination === "photos" ? (
                    <div className="selection-toolbar">
                      <button
                        onClick={() => {
                          setArchivePlan(undefined);
                          setSelectionEnabled((current) => !current);
                          setSelectedMedia(new Set());
                        }}
                        type="button"
                      >
                        {selectionEnabled
                          ? "Cancel selection"
                          : "Select photos"}
                      </button>
                      {selectionEnabled ? (
                        <button
                          disabled={
                            selectedMedia.size === 0 || archive.isPending
                          }
                          onClick={planSubset}
                          type="button"
                        >
                          {archive.isPending
                            ? "Preparing archive…"
                            : `Prepare archive for ${selectedMedia.size} selected`}
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                  {destination === "photos" &&
                  selectionEnabled &&
                  session.session_type === "public" ? (
                    <p className="archive-warning">
                      Subset archive files will remain on this public computer
                      after sign-out.
                    </p>
                  ) : null}
                  {destination === "photos" && archivePlan ? (
                    <ArchiveDownloads
                      csrfToken={session.csrf_token}
                      key={archivePlan.parts
                        .map((part) => part.download_url)
                        .join("|")}
                      plan={archivePlan}
                      publicComputer={session.session_type === "public"}
                    />
                  ) : null}
                  {dates.map((date, index) => (
                    <section id={`date-${index}`} key={date}>
                      <h2>{date}</h2>
                      <Gallery
                        media={media.filter(
                          (item) => mediaLabel(item) === date,
                        )}
                        onOpen={openMedia}
                        onToggle={toggleMedia}
                        selected={selectedMedia}
                        selectionEnabled={
                          destination === "photos" && selectionEnabled
                        }
                      />
                    </section>
                  ))}
                  {!photos.isPending && !photos.error && media.length === 0 ? (
                    <p className="library-empty">
                      {destination === "favorites"
                        ? "No Favorites yet."
                        : "No photos are available."}
                    </p>
                  ) : null}
                  {photos.hasNextPage ? (
                    <button
                      disabled={photos.isFetchingNextPage}
                      onClick={() => void photos.fetchNextPage()}
                      type="button"
                    >
                      {photos.isFetchingNextPage
                        ? "Loading…"
                        : "Load more photos"}
                    </button>
                  ) : null}
                </div>
              </div>
            )}
          </>
        )}
      </div>
      {openedMedia ? (
        <MediaViewer
          media={openedMedia}
          onClose={closeMedia}
          onOriginalDownload={() => {
            void recordEngagement(session, {
              kind: "original_download_started",
              media_item_id: openedMedia.id,
            });
          }}
          onVideoStarted={() => {
            void recordEngagement(session, {
              kind: "video_started",
              media_item_id: openedMedia.id,
            });
          }}
          refreshListingAccess={refreshListingAccess}
          session={session}
        />
      ) : null}
      <LibraryNavigation
        className="mobile-library-nav"
        current={openedEvent ? undefined : destination}
        onNavigate={navigateTo}
      />
    </section>
  );
}
