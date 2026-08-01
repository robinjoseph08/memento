import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { EventOrganizer } from "./EventOrganizer";
import { LooseItemOrganizer } from "./LooseItemOrganizer";
import type { SessionResponse } from "./types/generated/setup";

export function CurationWorkspace({
  session,
  onDirtyChange,
  onSavingChange,
}: {
  session: SessionResponse;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const requestedLoose = searchParams.has("loose");
  const requestedLooseID = searchParams.get("loose") ?? "";
  const requestedEventID = searchParams.get("event");
  const [looseSelected, setLooseSelected] = useState(requestedLoose);
  const rejectedKindRef = useRef<boolean | null>(null);
  const acceptedLooseIDRef = useRef(requestedLooseID);
  const acceptedEventIDRef = useRef(requestedEventID);
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);
  useEffect(() => onSavingChange?.(saving), [onSavingChange, saving]);
  useEffect(() => {
    if (requestedLoose === looseSelected) {
      rejectedKindRef.current = null;
      if (requestedLoose) acceptedLooseIDRef.current = requestedLooseID;
      else acceptedEventIDRef.current = requestedEventID;
      return;
    }
    if (rejectedKindRef.current === requestedLoose) return;
    if (
      saving ||
      (dirty &&
        !window.confirm("Discard changes that have not finished saving?"))
    ) {
      rejectedKindRef.current = requestedLoose;
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          next.set("workspace", "drafts");
          if (looseSelected) {
            next.delete("event");
            next.set("loose", acceptedLooseIDRef.current);
          } else {
            next.delete("loose");
            if (acceptedEventIDRef.current)
              next.set("event", acceptedEventIDRef.current);
            else next.delete("event");
          }
          return next;
        },
        { replace: true },
      );
      return;
    }
    const acceptNavigation = window.setTimeout(() => {
      setDirty(false);
      setLooseSelected(requestedLoose);
    });
    return () => window.clearTimeout(acceptNavigation);
  }, [
    dirty,
    looseSelected,
    requestedEventID,
    requestedLoose,
    requestedLooseID,
    saving,
    setSearchParams,
  ]);
  function choose(kind: "events" | "loose") {
    const alreadySelected = kind === "loose" ? looseSelected : !looseSelected;
    if (alreadySelected || saving) return;
    if (
      dirty &&
      !window.confirm("Discard changes that have not finished saving?")
    )
      return;
    setDirty(false);
    setLooseSelected(kind === "loose");
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.set("workspace", "drafts");
      if (kind === "events") next.delete("loose");
      else next.delete("event");
      if (kind === "loose" && !next.has("loose")) next.set("loose", "");
      return next;
    });
  }
  return (
    <>
      <nav aria-label="Curation kind" className="curation-kind-nav">
        <button
          aria-pressed={!looseSelected}
          disabled={saving}
          onClick={() => choose("events")}
          type="button"
        >
          Events
        </button>
        <button
          aria-pressed={looseSelected}
          disabled={saving}
          onClick={() => choose("loose")}
          type="button"
        >
          Loose items
        </button>
      </nav>
      {looseSelected ? (
        <LooseItemOrganizer
          onDirtyChange={setDirty}
          onSavingChange={setSaving}
          session={session}
        />
      ) : (
        <EventOrganizer
          onDirtyChange={setDirty}
          onSavingChange={setSaving}
          session={session}
        />
      )}
    </>
  );
}
