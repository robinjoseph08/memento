import { useEffect, useRef, useState } from "react";

import { usePrepareRecipientArchive } from "../hooks/queries/archives";

export function useSubsetArchiveModel(
  csrfToken: string,
  selectedMedia: Set<string>,
  selectionRevision: number,
) {
  const archive = usePrepareRecipientArchive(csrfToken);
  const preparing = useRef(false);
  const [preparedRevision, setPreparedRevision] = useState<number | null>(null);

  useEffect(() => {
    if (
      archive.isPending ||
      preparedRevision === null ||
      preparedRevision === selectionRevision ||
      (archive.data === undefined && archive.error === null)
    ) {
      return;
    }
    archive.reset();
  }, [archive, preparedRevision, selectionRevision]);

  const prepare = () => {
    if (preparing.current || archive.isPending || selectedMedia.size === 0) {
      return;
    }
    archive.reset();
    setPreparedRevision(selectionRevision);
    preparing.current = true;
    archive.mutate(
      {
        scope: "subset",
        event_id: null,
        media_ids: [...selectedMedia],
      },
      { onSettled: () => (preparing.current = false) },
    );
  };

  const reset = () => {
    setPreparedRevision(null);
    archive.reset();
  };
  const belongsToCurrentSelection = preparedRevision === selectionRevision;

  return {
    data: belongsToCurrentSelection ? archive.data : undefined,
    error: belongsToCurrentSelection ? archive.error : null,
    isPending: archive.isPending,
    prepare,
    reset,
  };
}

export type SubsetArchiveModel = ReturnType<typeof useSubsetArchiveModel>;
