import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";

import { apiJSON, apiNoContent } from "../../api";
import type {
  BodyRequest,
  Comment,
  ListResponse,
  ModerateRequest,
  MuteRequest,
} from "../../types/generated/comments";
import type { State as FavoriteState } from "../../types/generated/favorites";
import { favoriteQueryKey } from "./favorites";
import { isIdentityGenerationActive } from "./sessions";

export type CommentSubmission = BodyRequest & { idempotencyKey: string };
export type VersionedCommentBody = BodyRequest & {
  id: string;
  version: number;
};
export type CommentModeration = ModerateRequest & {
  id: string;
  version: number;
};
export type VersionedComment = { id: string; version: number };

export function commentsQueryKey(identityGeneration: string, mediaID: string) {
  return ["comments", identityGeneration, mediaID] as const;
}

export function useMediaComments(
  identityGeneration: string,
  mediaID: string,
  onUnavailable: (error: unknown) => void,
  isUnavailableResponse: (error: unknown) => boolean,
) {
  const queryClient = useQueryClient();
  const queryKey = commentsQueryKey(identityGeneration, mediaID);
  const identityIsActive = () =>
    isIdentityGenerationActive(queryClient, identityGeneration);
  const invalidateThread = () => {
    if (!identityIsActive()) return Promise.resolve();
    return queryClient.invalidateQueries({ queryKey });
  };

  async function verifyMediaAfterUnavailableComment(error: unknown) {
    if (!isUnavailableResponse(error) || !identityIsActive()) return;
    try {
      const state = await apiJSON<FavoriteState>(`/api/favorites/${mediaID}`);
      if (!identityIsActive()) return;
      queryClient.setQueryData(
        favoriteQueryKey(identityGeneration, mediaID),
        state,
      );
      await invalidateThread();
    } catch (recheckError) {
      if (identityIsActive()) onUnavailable(recheckError);
    }
  }

  const comments = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "50" });
      if (pageParam) params.set("cursor", pageParam);
      try {
        return await apiJSON<ListResponse>(
          `/api/comments/media/${mediaID}?${params.toString()}`,
        );
      } catch (error) {
        onUnavailable(error);
        throw error;
      }
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
  const create = useMutation({
    mutationFn: ({ body, idempotencyKey }: CommentSubmission) =>
      apiJSON<Comment>(`/api/comments/media/${mediaID}`, {
        method: "POST",
        headers: {
          "Idempotency-Key": idempotencyKey,
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify({ body } satisfies BodyRequest),
      }),
    onSuccess: invalidateThread,
    onError: onUnavailable,
  });
  const edit = useMutation({
    mutationFn: ({ id, body, version }: VersionedCommentBody) =>
      apiJSON<Comment>(`/api/comments/${id}`, {
        method: "PATCH",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify({ body } satisfies BodyRequest),
      }),
    onSuccess: invalidateThread,
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const remove = useMutation({
    mutationFn: ({ id, version }: VersionedComment) =>
      apiNoContent(`/api/comments/${id}`, {
        method: "DELETE",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": identityGeneration,
        },
      }),
    onSuccess: invalidateThread,
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const moderate = useMutation({
    mutationFn: ({ id, reason, version }: CommentModeration) =>
      apiNoContent(`/api/comments/${id}/moderate`, {
        method: "POST",
        headers: {
          "If-Match": String(version),
          "X-Memento-CSRF": identityGeneration,
        },
        body: JSON.stringify({ reason } satisfies ModerateRequest),
      }),
    onSuccess: invalidateThread,
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });
  const mute = useMutation({
    mutationFn: (muted: boolean) =>
      apiNoContent(`/api/comments/media/${mediaID}/mute`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": identityGeneration },
        body: JSON.stringify({ muted } satisfies MuteRequest),
      }),
    onSuccess: invalidateThread,
    onError: (error) => void verifyMediaAfterUnavailableComment(error),
  });

  return { comments, create, edit, remove, moderate, mute };
}
