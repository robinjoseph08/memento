// PROTOTYPE. Mounts the chosen album editor on the existing
// /curator/albums/:id route under `pnpm prototype:curator`. The Workbench and
// Ledger variants were dropped after the Outline layout was chosen.
import type { AlbumDetail } from "../../../types/generated/publishing";
import { ImportProgress } from "../../albums/import-progress";
import { AlbumEditor } from "./album-editor";
import type { PrototypeAlbum } from "./model";
import { PrototypeSwitcher } from "./switcher";

export function AlbumVariants({ album }: { album: AlbumDetail }) {
  // The stub serves the ticket 10 projections on top of today's AlbumDetail.
  const prototype = album as PrototypeAlbum;
  return (
    <>
      {album.status !== "complete" ? (
        <div className="px-5 min-[761px]:px-8">
          <ImportProgress album={album} />
        </div>
      ) : (
        <AlbumEditor album={prototype} />
      )}
      <PrototypeSwitcher albumID={album.id} name="Album editor" />
    </>
  );
}
