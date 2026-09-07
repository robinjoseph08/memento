import { useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { Connection } from "../../types/generated/immich";

export function useConnection(area: "setup" | "curator") {
  return useQuery({
    queryKey: ["connection", area],
    queryFn: ({ signal }) =>
      request<Connection>(`/api/${area}/connection`, { signal }),
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}
