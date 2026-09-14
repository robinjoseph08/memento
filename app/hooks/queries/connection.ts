import { useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { Connection } from "../../types/generated/immich";

// The diagnostic is a network call to Immich, so it runs on request rather
// than in the background. Pages that must not show a stale verdict, such as
// the dashboard, ask for a fresh check each time they mount.
export function useConnection(area: "setup" | "curator", fresh = false) {
  return useQuery({
    queryKey: ["connection", area],
    queryFn: ({ signal }) =>
      request<Connection>(`/api/${area}/connection`, { signal }),
    retry: false,
    staleTime: Infinity,
    refetchOnMount: fresh ? "always" : true,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}
