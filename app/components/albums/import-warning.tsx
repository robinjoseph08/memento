import { useConnection } from "../../hooks/queries/connection";
import { ReadFailure } from "../people/form-fields";

export function ImportWarning() {
  const connection = useConnection("curator");
  return (
    <div className="my-6 max-w-160">
      {connection.isError && (
        <ReadFailure
          error={connection.error}
          pending={connection.isFetching}
          retry={connection.refetch}
        />
      )}
      {connection.data &&
        (!connection.data.usable ||
          connection.data.import_supported === false) && (
          <p className="text-sm text-destructive" role="alert">
            {connection.data.message ||
              "This Immich version does not support importing. Update Immich before importing a new album."}
          </p>
        )}
    </div>
  );
}
