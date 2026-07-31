import { useRef, useState, type FormEvent } from "react";

import { useMediaComments } from "../hooks/queries/comments";
import { isUnavailableResponse } from "./mediaPresentation";
import { LibraryError } from "./presentation";

export function CommentThread({
  csrfToken,
  mediaID,
  unavailableMedia,
  onUnavailable,
}: {
  csrfToken: string;
  mediaID: string;
  unavailableMedia: boolean;
  onUnavailable: (error: unknown) => void;
}) {
  const commentRetry = useRef<{ body: string; key: string } | undefined>(
    undefined,
  );
  const [commentBody, setCommentBody] = useState("");
  const { comments, create, edit, remove, moderate, mute } = useMediaComments(
    csrfToken,
    mediaID,
    onUnavailable,
    isUnavailableResponse,
  );
  const commentItems =
    comments.data?.pages.flatMap((page) => page.comments) ?? [];
  const error =
    comments.error ??
    create.error ??
    edit.error ??
    remove.error ??
    moderate.error ??
    mute.error;

  function submitComment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const submission =
      commentRetry.current?.body === commentBody
        ? commentRetry.current
        : { body: commentBody, key: crypto.randomUUID() };
    commentRetry.current = submission;
    create.mutate(
      { body: submission.body, idempotencyKey: submission.key },
      {
        onSuccess: () => {
          commentRetry.current = undefined;
          setCommentBody("");
        },
      },
    );
  }

  return (
    <section aria-labelledby="comments-title" className="viewer-comments">
      <div className="viewer-comments-heading">
        <h3 id="comments-title">Comments</h3>
        <label>
          <input
            checked={comments.data?.pages[0]?.muted ?? false}
            disabled={
              unavailableMedia ||
              mute.isPending ||
              !(comments.data?.pages[0]?.can_mute ?? false)
            }
            onChange={(event) => mute.mutate(event.target.checked)}
            type="checkbox"
          />
          Mute future Comment notifications
        </label>
      </div>
      <LibraryError
        error={
          unavailableMedia || isUnavailableResponse(error)
            ? null
            : error instanceof Error
              ? error
              : null
        }
      />
      <ol aria-live="polite" className="comment-list">
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
                Comment moderated by {comment.moderator_name ?? "the Curator"}.
              </p>
            ) : (
              <p>{comment.body}</p>
            )}
            <div className="comment-actions">
              {comment.can_edit ? (
                <button
                  disabled={unavailableMedia || edit.isPending}
                  onClick={() => {
                    const body = window.prompt("Edit Comment", comment.body);
                    if (body?.trim()) {
                      edit.mutate({
                        id: comment.id,
                        body,
                        version: comment.version,
                      });
                    }
                  }}
                  type="button"
                >
                  Edit
                </button>
              ) : null}
              {comment.can_delete ? (
                <button
                  disabled={unavailableMedia || remove.isPending}
                  onClick={() => {
                    if (
                      window.confirm(
                        "Delete this Comment? This cannot be undone.",
                      )
                    ) {
                      remove.mutate({
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
                  disabled={unavailableMedia || moderate.isPending}
                  onClick={() => {
                    const reason = window.prompt("Moderation reason");
                    if (reason?.trim()) {
                      moderate.mutate({
                        id: comment.id,
                        reason,
                        version: comment.version,
                      });
                    }
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
      <form className="comment-form" onSubmit={submitComment}>
        <label>
          Add a Comment
          <textarea
            disabled={unavailableMedia}
            maxLength={2000}
            onChange={(event) => {
              const body = event.target.value;
              if (commentRetry.current?.body !== body) {
                commentRetry.current = undefined;
              }
              setCommentBody(body);
            }}
            required
            value={commentBody}
          />
        </label>
        <button
          disabled={unavailableMedia || create.isPending || !commentBody.trim()}
          type="submit"
        >
          {create.isPending ? "Posting…" : "Post Comment"}
        </button>
      </form>
    </section>
  );
}
