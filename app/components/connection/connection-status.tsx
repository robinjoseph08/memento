import { Plug, Unplug } from "lucide-react";

import { useConnection } from "../../hooks/queries/connection";
import { SectionHeading } from "../people/form-fields";
import {
  IntegrationStatus,
  type Verdict,
} from "../settings/integration-status";

export function ConnectionStatus() {
  return (
    <section
      aria-labelledby="connection-title"
      className="border-t border-border pt-7 min-[761px]:border-t-0 min-[761px]:border-l min-[761px]:pt-0 min-[761px]:pl-8"
    >
      <SectionHeading icon={Plug} id="connection-title">
        Immich connection
      </SectionHeading>
      <ConnectionDetails area="setup" />
    </section>
  );
}

export function ConnectionDetails({ area }: { area: "setup" | "curator" }) {
  const connection = useConnection(area);
  const verdict: Verdict | undefined = connection.data && {
    tone: connection.data.usable ? "usable" : "unusable",
    label: connection.data.usable ? "Connected" : "Not connected",
    message: connection.data.message,
    detail: connection.data.version
      ? `Version ${connection.data.version}`
      : undefined,
    icon: connection.data.usable ? undefined : Unplug,
  };
  return (
    <IntegrationStatus
      checking="Checking connection…"
      query={connection}
      verdict={verdict}
    >
      The Immich URL and API key are configured on the server. Your key is never
      shown here.
      {area === "setup" &&
        " You can claim this installation even if Immich is unavailable, then fix the connection later."}
    </IntegrationStatus>
  );
}
