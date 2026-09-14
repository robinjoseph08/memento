import { useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { Dashboard } from "../../types/generated/dashboard";
import { usePrivateScope } from "./people";

// The dashboard polls only while the server reports running work, and stops
// as soon as that work settles or the page is left. Each poll also recounts
// unannounced people, so the interval stays unhurried.
export function useDashboard() {
  const scope = usePrivateScope();
  return useQuery({
    queryKey: [...scope, "dashboard"],
    queryFn: ({ signal }) =>
      request<Dashboard>("/api/curator/dashboard", { signal }),
    retry: false,
    refetchOnWindowFocus: "always",
    refetchInterval: (query) => (query.state.data?.active ? 5000 : false),
  });
}
