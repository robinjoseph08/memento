import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import { apiJSON, apiNoContent } from "../../api";
import type {
  Event as EventDetail,
  EventPage,
  MediaPage,
  NewForYouResponse,
} from "../../types/generated/library";
import { isIdentityGenerationActive } from "./sessions";

export type RecipientMediaListing = "photos" | "favorites";

export function useRecipientMedia(
  identityGeneration: string,
  listing: RecipientMediaListing,
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey: ["recipient-library", identityGeneration, listing],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<MediaPage>(`/api/me/${listing}?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled,
    retry: false,
  });
}

export function useRecipientEvents(identityGeneration: string, enabled = true) {
  return useInfiniteQuery({
    queryKey: ["recipient-events", identityGeneration],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "24" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventPage>(`/api/me/events?${params.toString()}`);
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled,
    retry: false,
  });
}

export function useRecipientEvent(identityGeneration: string, eventID: string) {
  return useInfiniteQuery({
    queryKey: ["recipient-event", identityGeneration, eventID],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (pageParam) params.set("cursor", pageParam);
      return apiJSON<EventDetail>(
        `/api/me/events/${eventID}?${params.toString()}`,
      );
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    retry: false,
  });
}

export function useNewForYou(identityGeneration: string, enabled = true) {
  return useQuery({
    queryKey: ["new-for-you", identityGeneration],
    queryFn: () => apiJSON<NewForYouResponse>("/api/me/new-for-you"),
    enabled,
    retry: false,
  });
}

export function useMarkPublicationSeen(identityGeneration: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (publicationID: string) =>
      apiNoContent(`/api/me/new-for-you/${publicationID}/seen`, {
        method: "POST",
        headers: { "X-Memento-CSRF": identityGeneration },
      }),
    onSuccess: async () => {
      if (!isIdentityGenerationActive(queryClient, identityGeneration)) return;
      await queryClient.invalidateQueries({
        queryKey: ["new-for-you", identityGeneration],
      });
    },
  });
}
