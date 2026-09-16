import { useQuery } from "@tanstack/react-query";

import { request } from "../../lib/http";
import type { Status as ChapterStatus } from "../../types/generated/ffprobe";
import type { Connection } from "../../types/generated/immich";
import type { MailStatus } from "../../types/generated/notifications";

// Each diagnostic reaches out to a server or runs a process, so it runs on
// request rather than in the background. Pages that must not show a stale
// verdict, such as the dashboard, ask for a fresh check each time they mount.
function useDiagnostic<T>(queryKey: string[], path: string, fresh: boolean) {
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => request<T>(path, { signal }),
    retry: false,
    staleTime: Infinity,
    refetchOnMount: fresh ? "always" : true,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}

export function useConnection(area: "setup" | "curator", fresh = false) {
  return useDiagnostic<Connection>(
    ["connection", area],
    `/api/${area}/connection`,
    fresh,
  );
}

export function useEmailStatus() {
  return useDiagnostic<MailStatus>(["email"], "/api/curator/email", false);
}

export function useChapterStatus() {
  return useDiagnostic<ChapterStatus>(
    ["ffprobe"],
    "/api/curator/ffprobe",
    false,
  );
}
