import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiJSON } from "../../api";
import type { State as FavoriteState } from "../../types/generated/favorites";
import { isIdentityGenerationActive } from "./sessions";

export function favoriteQueryKey(identityGeneration: string, mediaID: string) {
  return ["favorite", identityGeneration, mediaID] as const;
}

export function useFavorite(
  identityGeneration: string,
  mediaID: string,
  onUnavailable: (error: unknown) => void,
) {
  const queryClient = useQueryClient();
  const queryKey = favoriteQueryKey(identityGeneration, mediaID);
  const favorite = useQuery({
    queryKey,
    queryFn: async () => {
      try {
        return await apiJSON<FavoriteState>(`/api/favorites/${mediaID}`);
      } catch (error) {
        onUnavailable(error);
        throw error;
      }
    },
    retry: false,
  });
  const toggle = useMutation({
    mutationFn: (next: boolean) =>
      apiJSON<FavoriteState>(`/api/favorites/${mediaID}`, {
        method: next ? "PUT" : "DELETE",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess: async (state) => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      queryClient.setQueryData(queryKey, state);
      await queryClient.invalidateQueries({
        queryKey: ["recipient-library", identityGeneration],
      });
    },
    onError: onUnavailable,
  });
  return { favorite, toggle };
}
