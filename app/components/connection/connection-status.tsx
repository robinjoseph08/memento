import { useConnection } from "../../hooks/queries/connection";
import { errorMessage } from "../../lib/http";
import { Button } from "../ui/button";

export function ConnectionStatus() {
  return (
    <section
      aria-labelledby="connection-title"
      className="border-t border-border pt-7 min-[761px]:border-t-0 min-[761px]:border-l min-[761px]:pt-0 min-[761px]:pl-8"
    >
      <h2
        className="font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]"
        id="connection-title"
      >
        Immich connection
      </h2>
      <ConnectionDetails area="setup" />
    </section>
  );
}

export function ConnectionDetails({ area }: { area: "setup" | "curator" }) {
  const connection = useConnection(area);
  return (
    <>
      <div
        aria-live="polite"
        className="mt-5.5 [&>p:first-child]:mb-2 [&>p:first-child]:font-medium"
      >
        {connection.isFetching ? (
          <p role="status">Checking connection…</p>
        ) : connection.isError ? (
          <p className="text-destructive" role="alert">
            {errorMessage(connection.error)}
          </p>
        ) : (
          <>
            <p
              className={
                connection.data?.usable
                  ? "text-accent-foreground"
                  : "text-destructive"
              }
            >
              {connection.data?.usable ? "Connected" : "Not connected"}
            </p>
            <p>{connection.data?.message}</p>
            {connection.data?.version && (
              <p className="mt-2.5 text-[11px]/[1.8] text-muted">
                Version {connection.data.version}
              </p>
            )}
          </>
        )}
      </div>
      <p className="my-5.5 text-xs/[1.8] text-muted">
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
