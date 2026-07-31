import { useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { CURRENT_SESSION_QUERY_KEY } from "./hooks/queries/sessions";
import { RecipientLibraryRoute } from "./recipient-library/RecipientLibraryRoute";
import type { SessionResponse } from "./types/generated/setup";

export { ArchiveDownloads } from "./recipient-library/ArchiveControls";

export function RecipientLibrary({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const location = useLocation();
  const navigate = useNavigate();

  useLayoutEffect(() => {
    queryClient.setQueryData(CURRENT_SESSION_QUERY_KEY, session);
  }, [queryClient, session]);

  return (
    <RecipientLibraryRoute
      key={session.csrf_token}
      navigatePath={(pathname) => void navigate(pathname)}
      pathname={location.pathname}
      session={session}
    />
  );
}
