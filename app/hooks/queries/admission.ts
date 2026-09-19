import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { request } from "../../lib/http";
import { statusKey } from "../../lib/query-client";
import type {
  AccessRequest,
  ApproveAccessRequestRequest,
  Invitation,
  Person,
  SendInvitationRequest,
  Status,
  UpdateProfileRequest,
} from "../../types/generated/identity";
import { useIdentityStatus } from "./identity";
import { usePrivateScope } from "./people";

export function useCompleteOnboarding() {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: UpdateProfileRequest) =>
      request<Person>("/api/identity/onboarding", { body }),
    onSuccess: async (person) => {
      const status = client.getQueryData<Status>(statusKey);
      if (!status || status.person?.id !== person.id) return;
      client.setQueryData<Status>(statusKey, { ...status, person });
      await client.invalidateQueries({ queryKey: scope });
    },
  });
}

export function useSendInvitation(personID: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (body: SendInvitationRequest) =>
      request<Invitation>(`/api/people/${personID}/invitations`, { body }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: [...scope, "person", personID] }),
  });
}

export function useRetryInvitation(personID: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: (invitationID: string) =>
      request<Invitation>(
        `/api/people/${personID}/invitations/${invitationID}/retry`,
        { body: {} },
      ),
    onSuccess: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: [...scope, "person", personID] }),
        client.invalidateQueries({ queryKey: [...scope, "dashboard"] }),
      ]);
    },
  });
}

// pendingRequestCount is the Curator's badge for Access Requests.
export function pendingRequestCount(
  requests: { status: string }[] | undefined,
) {
  if (!Array.isArray(requests)) return 0;
  return requests.filter((request) => request.status === "pending").length;
}

// Curators refresh the request list when they return to the window, so a
// request made by a stranger shows up without a manual reload.
export function useAccessRequests() {
  const scope = usePrivateScope();
  const { data } = useIdentityStatus();
  return useQuery({
    queryKey: [...scope, "access-requests"],
    queryFn: ({ signal }) =>
      request<AccessRequest[]>("/api/access-requests", { signal }),
    enabled: !!data?.person?.is_curator,
    staleTime: 30_000,
    refetchOnWindowFocus: "always",
    retry: false,
  });
}

function useAccessRequestAction(action: string) {
  const client = useQueryClient();
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: ({
      id,
      body = { person_id: "", display_name: "" },
    }: {
      id: string;
      body?: ApproveAccessRequestRequest;
    }) =>
      request<AccessRequest>(`/api/access-requests/${id}/${action}`, {
        body: action === "approve" ? body : {},
      }),
    // Invalidate without awaiting: the row moves between lists once the
    // refetch lands, and a caller's own onSuccess must run before that unmount.
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: [...scope, "access-requests"],
      });
      void client.invalidateQueries({ queryKey: [...scope, "people"] });
      void client.invalidateQueries({ queryKey: [...scope, "dashboard"] });
    },
  });
}

export function useApproveAccessRequest() {
  return useAccessRequestAction("approve");
}
export function useDenyAccessRequest() {
  return useAccessRequestAction("deny");
}
export function useReconsiderAccessRequest() {
  return useAccessRequestAction("reconsider");
}

export function useRequestAlbumAccess(albumID: string) {
  const scope = usePrivateScope();
  return useMutation({
    mutationKey: scope,
    mutationFn: () =>
      request<AccessRequest>(
        `/api/albums/${encodeURIComponent(albumID)}/request-access`,
        { body: {} },
      ),
  });
}
