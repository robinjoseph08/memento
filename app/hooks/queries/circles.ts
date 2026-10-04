import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type {
  Circle,
  CircleMembersRequest,
  CircleRequest,
  PersonCirclesRequest,
} from "../../types/generated/identity";
import { usePrivateScope } from "./people";

export function useCircles() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "circles"],
    queryFn: ({ signal }) => request<Circle[]>("/api/circles", { signal }),
    retry: false,
  });
}

// Membership shows on the Circles page, each Person's details, and every
// Album's Offers, which decide who an Album reaches, so every Circle change
// refreshes them all. A failure refreshes them too, since it usually means a
// Circle changed elsewhere.
function useCircleMutation<T, V>(fn: (variables: V) => Promise<T>) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: fn,
    onSettled: () =>
      Promise.all([
        client.invalidateQueries({ queryKey: [...scope, "circles"] }),
        client.invalidateQueries({ queryKey: [...scope, "person"] }),
        client.invalidateQueries({ queryKey: [...scope, "album"] }),
        client.invalidateQueries({ queryKey: [...scope, "albums"] }),
        client.invalidateQueries({ queryKey: [...scope, "publication"] }),
      ]),
  });
}

export function useCreateCircle() {
  return useCircleMutation((body: CircleRequest) =>
    request<Circle>("/api/circles", { body }),
  );
}

export function useRenameCircle(id: string) {
  return useCircleMutation((body: CircleRequest) =>
    request<Circle>(`/api/circles/${id}`, { body }),
  );
}

export function useDeleteCircle(id: string) {
  return useCircleMutation(() =>
    request<void>(`/api/circles/${id}/delete`, { body: {} }),
  );
}

export function useSetCircleMembers(id: string) {
  return useCircleMutation((body: CircleMembersRequest) =>
    request<Circle>(`/api/circles/${id}/members`, { body }),
  );
}

export function useSetPersonCircles(personID: string) {
  return useCircleMutation((body: PersonCirclesRequest) =>
    request<void>(`/api/people/${personID}/circles`, { body }),
  );
}
