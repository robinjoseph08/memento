import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";

import { apiJSON, apiNoContent } from "./api";
import type {
  Comment,
  CuratorListResponse as CommentPage,
  HistoryResponse,
} from "./types/generated/comments";
import type { CuratorListResponse as FavoritePage } from "./types/generated/favorites";
import type { CuratorMedia } from "./types/generated/library";
import type { ListResponse as PeopleResponse } from "./types/generated/people";
import type { SessionResponse } from "./types/generated/setup";

function InteractionError({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

function commentText(comment: Comment) {
  if (comment.state === "deleted") return "Comment deleted by its author.";
  if (comment.state === "moderated")
    return `Comment moderated by ${comment.moderator_name ?? "the Curator"}.`;
  return comment.body;
}

function CuratorMediaViewer({
  mediaID,
  onClose,
}: {
  mediaID: string;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const media = useQuery({
    queryKey: ["curator-media-context", mediaID],
    queryFn: () => apiJSON<CuratorMedia>(`/api/curator/media/${mediaID}`),
    retry: false,
  });

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (typeof dialog.showModal === "function") {
      if (!dialog.open) dialog.showModal();
    } else if (!dialog.open) {
      dialog.setAttribute("open", "");
    }
  }, []);

  function close() {
    if (dialogRef.current && typeof dialogRef.current.close === "function") {
      dialogRef.current.close();
    } else {
      onClose();
    }
  }

  const context = media.data;
  const label = context?.filename || "Media item";
  return (
    <dialog
      aria-labelledby="curator-media-context-title"
      aria-modal="true"
      className="curator-media-viewer"
      onClose={onClose}
      ref={dialogRef}
    >
      <header>
        <div>
          <p className="step-label">Curator-only moderation context</p>
          <h3 id="curator-media-context-title">{label}</h3>
        </div>
        <button autoFocus onClick={close} type="button">
          Close Media context
        </button>
      </header>
      <InteractionError error={media.error} />
      {media.isPending ? <p>Loading Media context…</p> : null}
      {context ? (
        <>
          <div className="curator-media-preview">
            {!context.available ? (
              <p>Source unavailable</p>
            ) : context.media_type === "video" ? (
              <video
                aria-label={`Moderation preview for ${label}`}
                controls
                playsInline
                poster={context.thumbnail_url}
                preload="metadata"
                src={context.video_url}
              />
            ) : (
              <img
                alt={`Moderation preview for ${label}`}
                src={context.preview_url}
              />
            )}
          </div>
          <dl className="curator-media-metadata">
            <div>
              <dt>Portal Media ID</dt>
              <dd>{context.id}</dd>
            </div>
            <div>
              <dt>Type</dt>
              <dd>{context.media_type}</dd>
            </div>
            <div>
              <dt>Dimensions</dt>
              <dd>
                {context.width && context.height
                  ? `${context.width} × ${context.height}`
                  : "Unknown"}
              </dd>
            </div>
            <div>
              <dt>Capture time</dt>
              <dd>
                {context.local_date_time
                  ? new Date(context.local_date_time).toLocaleString()
                  : "Unknown"}
              </dd>
            </div>
            <div>
              <dt>Current Events</dt>
              <dd>
                {context.event_titles.length > 0
                  ? context.event_titles.join(", ")
                  : "Not in a current Event"}
              </dd>
            </div>
          </dl>
        </>
      ) : null}
    </dialog>
  );
}

export function CuratorInteractions({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [opened, setOpened] = useState(false);
  const [recipientID, setRecipientID] = useState("");
  const [historyCommentID, setHistoryCommentID] = useState("");
  const [mediaContextID, setMediaContextID] = useState("");
  const comments = useInfiniteQuery({
    queryKey: ["curator-comments", session.csrf_token],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<CommentPage>(`/api/comments/curator?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: session.curator && opened,
    retry: false,
  });
  const people = useQuery({
    queryKey: ["curator-interaction-people"],
    queryFn: () =>
      apiJSON<PeopleResponse>("/api/people?query=&include_archived=false"),
    enabled: session.curator && opened,
    retry: false,
  });
  const favorites = useInfiniteQuery({
    queryKey: ["curator-favorites", session.csrf_token, recipientID],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<FavoritePage>(
        `/api/favorites/curator/recipients/${recipientID}?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: session.curator && opened && Boolean(recipientID),
    retry: false,
  });
  const history = useInfiniteQuery({
    queryKey: ["curator-comment-history", historyCommentID],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<HistoryResponse>(
        `/api/comments/${historyCommentID}/moderation-history?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: session.curator && opened && Boolean(historyCommentID),
    retry: false,
  });
  const moderate = useMutation({
    mutationFn: ({ comment, reason }: { comment: Comment; reason: string }) =>
      apiNoContent(`/api/comments/${comment.id}/moderate`, {
        method: "POST",
        headers: {
          "If-Match": String(comment.version),
          "X-Memento-CSRF": session.csrf_token,
        },
        body: JSON.stringify({ reason }),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["curator-comments"] });
    },
  });

  const commentItems = useMemo(
    () => comments.data?.pages.flatMap((page) => page.comments) ?? [],
    [comments.data],
  );
  const favoriteIDs = useMemo(
    () => favorites.data?.pages.flatMap((page) => page.media_item_ids) ?? [],
    [favorites.data],
  );
  const recipients =
    people.data?.people.filter((person) =>
      person.roles.includes("recipient"),
    ) ?? [];
  const historyItems =
    history.data?.pages.flatMap((page) => page.history) ?? [];

  if (!session.curator) return null;

  return (
    <details
      className="shell-card curator-card curator-interactions"
      onToggle={(event) => setOpened(event.currentTarget.open)}
    >
      <summary>
        <span className="eyebrow">MEMENTO CURATOR</span>
        <strong id="curator-interactions-title">Comments and Favorites</strong>
      </summary>
      {opened ? (
        <>
          <p>
            Moderate private Comments and inspect one Recipient&apos;s private
            Favorites at a time.
          </p>
          <div className="curator-interaction-grid">
            <section aria-labelledby="curator-comments-title">
              <h3 id="curator-comments-title">Recent Comments</h3>
              <InteractionError error={comments.error ?? moderate.error} />
              <ol className="curator-comment-list">
                {commentItems.map((comment) => (
                  <li key={comment.id}>
                    <button
                      aria-label={`View Media context for ${comment.author_name}'s Comment`}
                      className="curator-media-trigger"
                      onClick={() => setMediaContextID(comment.media_item_id)}
                      type="button"
                    >
                      <img
                        alt=""
                        loading="lazy"
                        src={`/api/curator/media/${comment.media_item_id}/thumbnail`}
                      />
                    </button>
                    <div>
                      <strong>{comment.author_name}</strong>
                      <time dateTime={comment.created_at}>
                        {new Date(comment.created_at).toLocaleString()}
                      </time>
                      <p>{commentText(comment)}</p>
                      <div className="comment-actions">
                        {comment.can_moderate ? (
                          <button
                            disabled={moderate.isPending}
                            onClick={() => {
                              const reason = window.prompt("Moderation reason");
                              if (reason?.trim())
                                moderate.mutate({ comment, reason });
                            }}
                            type="button"
                          >
                            Moderate Comment
                          </button>
                        ) : null}
                        {comment.state === "moderated" ? (
                          <button
                            aria-expanded={historyCommentID === comment.id}
                            onClick={() =>
                              setHistoryCommentID((current) =>
                                current === comment.id ? "" : comment.id,
                              )
                            }
                            type="button"
                          >
                            {historyCommentID === comment.id
                              ? "Hide moderation history"
                              : "Show moderation history"}
                          </button>
                        ) : null}
                      </div>
                      {historyCommentID === comment.id ? (
                        <div className="moderation-history">
                          <InteractionError error={history.error} />
                          {historyItems.map((entry, index) => (
                            <article key={`${entry.created_at}-${index}`}>
                              <strong>{entry.actor_name}</strong>:{" "}
                              {entry.reason}
                              <blockquote>{entry.prior_body}</blockquote>
                            </article>
                          ))}
                          {history.hasNextPage ? (
                            <button
                              disabled={history.isFetchingNextPage}
                              onClick={() => void history.fetchNextPage()}
                              type="button"
                            >
                              {history.isFetchingNextPage
                                ? "Loading…"
                                : "Load more moderation history"}
                            </button>
                          ) : null}
                        </div>
                      ) : null}
                    </div>
                  </li>
                ))}
              </ol>
              {comments.isSuccess && commentItems.length === 0 ? (
                <p>No Comments have been posted.</p>
              ) : null}
              {comments.hasNextPage ? (
                <button
                  disabled={comments.isFetchingNextPage}
                  onClick={() => void comments.fetchNextPage()}
                  type="button"
                >
                  {comments.isFetchingNextPage
                    ? "Loading…"
                    : "Load older Comments"}
                </button>
              ) : null}
            </section>

            <section aria-labelledby="curator-favorites-title">
              <h3 id="curator-favorites-title">Recipient Favorites</h3>
              <label>
                Recipient
                <select
                  onChange={(event) => setRecipientID(event.target.value)}
                  value={recipientID}
                >
                  <option value="">Choose a Recipient</option>
                  {recipients.map((recipient) => (
                    <option key={recipient.id} value={recipient.id}>
                      {recipient.display_name}
                    </option>
                  ))}
                </select>
              </label>
              <InteractionError error={people.error ?? favorites.error} />
              <ul className="curator-favorite-list">
                {favoriteIDs.map((mediaID) => (
                  <li key={mediaID}>
                    <button
                      aria-label="View Media context for this Favorite"
                      className="curator-media-trigger"
                      onClick={() => setMediaContextID(mediaID)}
                      type="button"
                    >
                      <img
                        alt=""
                        loading="lazy"
                        src={`/api/curator/media/${mediaID}/thumbnail`}
                      />
                      <span>View Media context</span>
                    </button>
                  </li>
                ))}
              </ul>
              {recipientID &&
              favorites.isSuccess &&
              favoriteIDs.length === 0 ? (
                <p>This Recipient has no current Favorites.</p>
              ) : null}
              {favorites.hasNextPage ? (
                <button
                  disabled={favorites.isFetchingNextPage}
                  onClick={() => void favorites.fetchNextPage()}
                  type="button"
                >
                  {favorites.isFetchingNextPage
                    ? "Loading…"
                    : "Load more Favorites"}
                </button>
              ) : null}
            </section>
          </div>
          {mediaContextID ? (
            <CuratorMediaViewer
              mediaID={mediaContextID}
              onClose={() => setMediaContextID("")}
            />
          ) : null}
        </>
      ) : null}
    </details>
  );
}
