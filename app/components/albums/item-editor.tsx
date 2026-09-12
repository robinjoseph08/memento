import { useLayoutEffect, useRef, useState } from "react";

import type { Entry } from "../../types/generated/publishing";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { AccessRules } from "./access-rules";
import { AlbumImage } from "./album-image";
import { ScopeAccess } from "./scope-access";

export function ItemEditor({
  albumID,
  entry,
  inheritedAllows,
  onClose,
}: {
  albumID: string;
  entry: Entry;
  inheritedAllows: string[];
  onClose: () => void;
}) {
  const [rulesOpen, setRulesOpen] = useState(false);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (document.activeElement instanceof HTMLElement)
      returnFocusRef.current = document.activeElement;
  }, []);
  return (
    <Dialog onOpenChange={(open) => !open && onClose()} open>
      <DialogContent
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          if (returnFocusRef.current?.isConnected)
            returnFocusRef.current.focus();
        }}
      >
        <DialogTitle>Item access</DialogTitle>
        <DialogDescription className="mt-2 text-sm text-muted">
          Access applies only to this item in this Album.
        </DialogDescription>
        <AlbumImage
          alt=""
          className="mt-4 h-auto max-h-48 w-auto max-w-full"
          fallback="No preview available"
          src={entry.available ? entry.thumbnail_url : ""}
        />
        <dl className="my-5 text-sm">
          <dt className="text-xs text-muted">Filename</dt>
          <dd className="wrap-anywhere">{entry.filename}</dd>
          <dt className="mt-3 text-xs text-muted">Captured</dt>
          <dd>{entry.captured_at.replace("T", " ")}</dd>
          <dt className="mt-3 text-xs text-muted">Source</dt>
          <dd>
            {entry.available ? "Available in Immich" : "Unavailable in Immich"}
          </dd>
        </dl>
        <ScopeAccess
          albumID={albumID}
          entryID={entry.id}
          inheritedAllows={inheritedAllows}
          people={entry.access}
          total={1}
        />
        <Button onClick={() => setRulesOpen(true)} variant="outline">
          Rules & exceptions
        </Button>
        {rulesOpen && (
          <AccessRules
            albumID={albumID}
            inheritedAllows={inheritedAllows}
            onClose={() => setRulesOpen(false)}
            people={entry.access}
            target="entries"
            targetID={entry.id}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
