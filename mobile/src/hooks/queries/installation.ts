import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { findInstallation } from "@/lib/installation";
import { useConnection, useCreateHTTP } from "@/providers";

// Query keys start with the Installation origin, so one Installation's server
// state can be dropped without touching another's.
function installationStatusKey(origin: string) {
  return [origin, "installation-status"];
}

// useConnect checks an address against its status endpoint and, when Memento
// answers, connects the app to it.
export function useConnect() {
  const createHTTP = useCreateHTTP();
  const { connect } = useConnection();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (origin: string) => findInstallation(createHTTP(origin)),
    onSuccess: async (found) => {
      queryClient.setQueryData(installationStatusKey(found.origin), found);
      await connect(found.origin);
    },
  });
}

// useInstallationStatus repeats the status check for the connected
// Installation, which may have been upgraded, or gone away, since last time.
export function useInstallationStatus(origin: string) {
  const createHTTP = useCreateHTTP();
  return useQuery({
    queryKey: installationStatusKey(origin),
    queryFn: ({ signal }) => findInstallation(createHTTP(origin), signal),
    retry: false,
  });
}
