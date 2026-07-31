import {
  useMarkPublicationSeen,
  useNewForYou,
} from "../hooks/queries/recipientLibrary";

export function useNewForYouModel(csrfToken: string, enabled: boolean) {
  return {
    newForYou: useNewForYou(csrfToken, enabled),
    seen: useMarkPublicationSeen(csrfToken),
  };
}

export type NewForYouModel = ReturnType<typeof useNewForYouModel>;
