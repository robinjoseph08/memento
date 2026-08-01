import {
  useMarkPublicationSeen,
  useNewForYou,
  useRecipientLooseItemAccess,
} from "../hooks/queries/recipientLibrary";

export function useNewForYouModel(csrfToken: string, enabled: boolean) {
  return {
    newForYou: useNewForYou(csrfToken, enabled),
    refreshLooseItemAccess: useRecipientLooseItemAccess(csrfToken),
    seen: useMarkPublicationSeen(csrfToken),
  };
}

export type NewForYouModel = ReturnType<typeof useNewForYouModel>;
