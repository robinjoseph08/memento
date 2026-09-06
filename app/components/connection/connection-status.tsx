import { useConnection } from "../../hooks/queries/connection";
import { errorMessage } from "../../lib/http";
import { Button } from "../ui/button";

export function ConnectionStatus() {
  return (
    <section aria-labelledby="connection-title" className="connection-status">
      <h2 id="connection-title">Immich connection</h2>
      <ConnectionDetails area="setup" />
    </section>
  );
}

export function ConnectionDetails({ area }: { area: "setup" | "curator" }) {
  const connection = useConnection(area);
  return (
    <>
      <div aria-live="polite" className="connection-result">
        {connection.isFetching ? (
          <p role="status">Checking connection…</p>
        ) : connection.isError ? (
          <p role="alert">{errorMessage(connection.error)}</p>
        ) : (
          <>
            <p className={connection.data?.usable ? "connection-ok" : ""}>
              {connection.data?.usable
                ? "Connected"
                : "Connection needs attention"}
            </p>
            <p>{connection.data?.message}</p>
            {connection.data?.version && (
              <p className="connection-version">
                Version {connection.data.version}
              </p>
            )}
          </>
        )}
      </div>
      <p className="connection-help">
        The Immich URL and API key are configured on the server. Your key is
        never shown here.
        {area === "setup" &&
          " You can claim this installation even if Immich is unavailable, then fix the connection later."}
      </p>
      <Button
        disabled={connection.isFetching}
        onClick={() => void connection.refetch()}
        variant="outline"
      >
        {connection.isFetching ? "Checking…" : "Check again"}
      </Button>
    </>
  );
}
