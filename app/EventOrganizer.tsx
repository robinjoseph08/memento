import { useEffect, useEffectEvent, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { APIError } from "./api";
import { AudienceInspection } from "./event-workspace/AudienceInspection";
import { EventOrganization } from "./event-workspace/EventOrganization";
import { validateEventMetadata } from "./event-workspace/eventMetadata";
import { Publication } from "./event-workspace/Publication";
import { StagedUpdateReview } from "./event-workspace/StagedReview";
import { Withdrawal } from "./event-workspace/Withdrawal";
import {
  useEvent,
  useEvents,
  useOrganizeEvent,
  useRestorePublishedMedia,
  type EventMutationAttempt,
  type PublishEventAttempt,
  type WithdrawEventAttempt,
} from "./hooks/queries/events";
import type {
  Event as DraftEvent,
  PublicationResponse,
  Withdrawal as WithdrawalResult,
} from "./types/generated/events";
import type { SessionResponse } from "./types/generated/setup";

type Pane = "work" | "organize" | "inspect";
type SaveState = "saved" | "saving" | "unsaved" | "failed" | "conflict";

function cloneEvent<T>(value: T): T {
  return structuredClone(value);
}

function sameIDs(left: { id: string }[], right: { id: string }[]) {
  return (
    left.length === right.length &&
    left.every((item, index) => item.id === right[index].id)
  );
}

function sameStrings(left: string[], right: string[]) {
  return (
    left.length === right.length &&
    left.every((value, index) => value === right[index])
  );
}

function insertByServerOrder<T extends { id: string }>(
  items: T[],
  item: T,
  serverItems: T[],
) {
  const serverIndex = serverItems.findIndex(
    (candidate) => candidate.id === item.id,
  );
  const successor = serverItems
    .slice(serverIndex + 1)
    .find((candidate) => items.some((current) => current.id === candidate.id));
  const insertionIndex = successor
    ? items.findIndex((current) => current.id === successor.id)
    : items.length;
  items.splice(insertionIndex, 0, cloneEvent(item));
}

function eventMediaIDs(event: DraftEvent) {
  return new Set(
    event.moments
      .flatMap((moment) => moment.media_items)
      .concat(event.unassigned_media)
      .map((item) => item.id),
  );
}

function rebaseOrganization(
  base: DraftEvent,
  local: DraftEvent,
  serverResponse: DraftEvent,
) {
  // A separately persisted review can finish after a requested mutation but
  // before its response arrives. The newer local snapshot is authoritative.
  const server =
    local.version > serverResponse.version ? local : serverResponse;
  const rebased = cloneEvent(server);
  if (local.title !== base.title) rebased.title = local.title;
  if (local.description !== base.description)
    rebased.description = local.description;
  if (local.date_start !== base.date_start)
    rebased.date_start = local.date_start;
  if (local.date_end !== base.date_end) rebased.date_end = local.date_end;
  if (local.selected_cover_media_item_id !== base.selected_cover_media_item_id)
    rebased.selected_cover_media_item_id = local.selected_cover_media_item_id;
  if (!sameStrings(local.place_labels, base.place_labels))
    rebased.place_labels = cloneEvent(local.place_labels);
  if (local.grouping_timezone !== base.grouping_timezone)
    rebased.grouping_timezone = local.grouping_timezone;
  if (local.final_review_complete !== base.final_review_complete)
    rebased.final_review_complete = local.final_review_complete;

  const baseMoments = new Map(
    base.moments.map((moment) => [moment.id, moment]),
  );
  const localMoments = new Map(
    local.moments.map((moment) => [moment.id, moment]),
  );
  const serverMoments = new Map(
    server.moments.map((moment) => [moment.id, moment]),
  );
  const baseMedia = eventMediaIDs(base);
  const localMedia = eventMediaIDs(local);

  let momentOrder = server.moments.map((moment) => moment.id);
  if (!sameIDs(local.moments, base.moments)) {
    momentOrder = local.moments.map((moment) => moment.id);
    for (const serverMoment of server.moments) {
      if (baseMoments.has(serverMoment.id) || localMoments.has(serverMoment.id))
        continue;
      const ordered = momentOrder.map((id) => ({ id }));
      insertByServerOrder(ordered, { id: serverMoment.id }, server.moments);
      momentOrder = ordered.map((item) => item.id);
    }
  }

  rebased.moments = momentOrder.flatMap((momentID) => {
    const baseMoment = baseMoments.get(momentID);
    const localMoment = localMoments.get(momentID);
    const serverMoment = serverMoments.get(momentID);
    if (!localMoment) return serverMoment ? [cloneEvent(serverMoment)] : [];
    if (!baseMoment) return [cloneEvent(localMoment)];
    if (!serverMoment) return [];

    const merged = cloneEvent(serverMoment);
    if (localMoment.title !== baseMoment.title)
      merged.title = localMoment.title;
    if (!sameStrings(localMoment.place_labels, baseMoment.place_labels))
      merged.place_labels = cloneEvent(localMoment.place_labels);
    if (localMoment.proposed_day !== baseMoment.proposed_day)
      merged.proposed_day = localMoment.proposed_day;
    if (localMoment.cover_media_item_id !== baseMoment.cover_media_item_id)
      merged.cover_media_item_id = localMoment.cover_media_item_id;
    if (localMoment.attendance_complete !== baseMoment.attendance_complete)
      merged.attendance_complete = localMoment.attendance_complete;
    if (localMoment.audience_complete !== baseMoment.audience_complete)
      merged.audience_complete = localMoment.audience_complete;

    if (!sameIDs(localMoment.media_items, baseMoment.media_items)) {
      merged.media_items = cloneEvent(localMoment.media_items);
      for (const serverItem of serverMoment.media_items) {
        if (baseMedia.has(serverItem.id) || localMedia.has(serverItem.id))
          continue;
        insertByServerOrder(
          merged.media_items,
          serverItem,
          serverMoment.media_items,
        );
      }
    }
    return [merged];
  });

  if (!sameIDs(local.unassigned_media, base.unassigned_media)) {
    rebased.unassigned_media = cloneEvent(local.unassigned_media);
    for (const serverItem of server.unassigned_media) {
      if (baseMedia.has(serverItem.id) || localMedia.has(serverItem.id))
        continue;
      insertByServerOrder(
        rebased.unassigned_media,
        serverItem,
        server.unassigned_media,
      );
    }
  }

  // Preserve a local Moment deletion while keeping newly restored Media
  // available for the Curator to place again.
  const rebasedMedia = eventMediaIDs(rebased);
  const serverMedia = server.moments
    .flatMap((moment) => moment.media_items)
    .concat(server.unassigned_media);
  for (const serverItem of serverMedia) {
    if (baseMedia.has(serverItem.id) || rebasedMedia.has(serverItem.id))
      continue;
    rebased.unassigned_media.push(cloneEvent(serverItem));
    rebasedMedia.add(serverItem.id);
  }
  return rebased;
}

function applyLocalOrganization(local: DraftEvent, server: DraftEvent) {
  const next = cloneEvent(server);
  next.title = local.title;
  next.description = local.description;
  next.date_start = local.date_start;
  next.date_end = local.date_end;
  next.selected_cover_media_item_id = local.selected_cover_media_item_id;
  next.place_labels = cloneEvent(local.place_labels);
  next.grouping_timezone = local.grouping_timezone;
  next.final_review_complete = local.final_review_complete;
  const serverMoments = new Map(
    server.moments.map((moment) => [moment.id, moment]),
  );
  next.moments = local.moments.map((moment) => {
    const authoritative = serverMoments.get(moment.id);
    return {
      ...cloneEvent(moment),
      attendance_complete:
        authoritative?.attendance_complete ?? moment.attendance_complete,
      audience_complete:
        authoritative?.audience_complete ?? moment.audience_complete,
    };
  });
  next.unassigned_media = cloneEvent(local.unassigned_media);
  const retainedMedia = eventMediaIDs(next);
  for (const item of server.moments
    .flatMap((moment) => moment.media_items)
    .concat(server.unassigned_media)) {
    if (retainedMedia.has(item.id)) continue;
    next.unassigned_media.push(cloneEvent(item));
    retainedMedia.add(item.id);
  }
  return next;
}

export function EventOrganizer({
  session,
  onDirtyChange,
  onSavingChange,
}: {
  session: SessionResponse;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
}) {
  const identityGeneration = session.csrf_token;
  const [searchParams, setSearchParams] = useSearchParams();
  const looseKindRequested = searchParams.has("loose");
  const requestedEventID = searchParams.get("event") ?? "";
  const [selectedID, setSelectedID] = useState(requestedEventID);
  const [draft, setDraft] = useState<DraftEvent>();
  const [inspectedMomentID, setInspectedMomentID] = useState("");
  const [activePane, setActivePane] = useState<Pane>(() =>
    requestedEventID ? "organize" : "work",
  );
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [conflictRecoveryError, setConflictRecoveryError] = useState("");
  const [conflictRecoveryPending, setConflictRecoveryPending] = useState(false);
  const [restoreRecoveryError, setRestoreRecoveryError] = useState("");
  const [restoreRecoveryPending, setRestoreRecoveryPending] = useState(false);
  const [restoreStatus, setRestoreStatus] = useState("");
  const [revision, setRevision] = useState(0);
  const [previewResetGeneration, setPreviewResetGeneration] = useState(0);
  const [publicationPending, setPublicationPending] = useState(false);
  const [withdrawalPending, setWithdrawalPending] = useState(false);
  const [authorityRefreshRequired, setAuthorityRefreshRequired] =
    useState(false);
  const [authorityRefreshError, setAuthorityRefreshError] = useState("");
  const revisionRef = useRef(0);
  const selectionGenerationRef = useRef(0);
  const latestDraftRef = useRef<DraftEvent | undefined>(undefined);
  const selectedIDRef = useRef(selectedID);
  const localDuringConflict = useRef<DraftEvent | undefined>(undefined);
  const authorityRecoveryAttemptRef = useRef<EventMutationAttempt | undefined>(
    undefined,
  );
  const workPaneRef = useRef<HTMLElement>(null);
  const organizePaneRef = useRef<HTMLElement>(null);
  const inspectPaneRef = useRef<HTMLElement>(null);

  const work = useEvents(identityGeneration);
  const eventQuery = useEvent(identityGeneration, selectedID);
  const serverDraft =
    eventQuery.data?.id === selectedID ? eventQuery.data : undefined;
  const localDraft = draft?.id === selectedID ? draft : undefined;
  const currentDraft = authorityRefreshRequired
    ? (localDraft ?? serverDraft)
    : saveState === "saved"
      ? (serverDraft ?? localDraft)
      : (localDraft ?? serverDraft);
  const metadata = validateEventMetadata(currentDraft);

  const save = useOrganizeEvent(identityGeneration, {
    onMutate: () => {
      setSaveState("saving");
      onSavingChange?.(true);
    },
    onSuccess: (saved, attempted) => {
      if (!ownsAttempt(attempted)) return;
      onSavingChange?.(false);
      const latest = latestDraftRef.current;
      const hasNewerEdits = revisionRef.current > attempted.revision;
      const hasNewerAuthoritativeState =
        latest?.id === saved.id && latest.version > saved.version;
      if (
        latest?.id === saved.id &&
        (hasNewerEdits || hasNewerAuthoritativeState)
      ) {
        const rebased = rebaseOrganization(attempted.event, latest, saved);
        latestDraftRef.current = rebased;
        setDraft(rebased);
        setSaveState(hasNewerEdits ? "unsaved" : "saved");
        if (!hasNewerEdits) {
          revisionRef.current = 0;
          setRevision(0);
        }
        return;
      }
      const next = cloneEvent(saved);
      latestDraftRef.current = next;
      setDraft(next);
      setSaveState("saved");
      revisionRef.current = 0;
      setRevision(0);
    },
    onError: (error, attempted) => {
      if (!ownsAttempt(attempted)) return;
      onSavingChange?.(false);
      if (error instanceof APIError && error.status === 409) {
        const latest = latestDraftRef.current;
        localDuringConflict.current = cloneEvent(
          latest?.id === attempted.event.id ? latest : attempted.event,
        );
        setConflictRecoveryError("");
        setSaveState("conflict");
      } else {
        setSaveState("failed");
      }
    },
  });

  const restorePublishedMedia = useRestorePublishedMedia(identityGeneration, {
    onMutate: () => setRestoreStatus(""),
    onSuccess: (restored, attempted) => {
      setRestoreRecoveryError("");
      setPreviewResetGeneration((current) => current + 1);
      if (!ownsAttempt(attempted)) return;
      const latest = latestDraftRef.current;
      const hasNewerOrganization = revisionRef.current > attempted.revision;
      const next =
        latest?.id === restored.id
          ? rebaseOrganization(attempted.event, latest, restored)
          : cloneEvent(restored);
      const originalMoment = restored.moments.find((moment) =>
        moment.media_items.some((item) => item.id === attempted.mediaID),
      );
      const relocatedToUnassigned =
        originalMoment !== undefined &&
        latest?.id === restored.id &&
        !latest.moments.some((moment) => moment.id === originalMoment.id) &&
        next.unassigned_media.some((item) => item.id === attempted.mediaID);
      latestDraftRef.current = next;
      setDraft(next);
      setSaveState(hasNewerOrganization ? "unsaved" : "saved");
      if (!hasNewerOrganization) {
        revisionRef.current = 0;
        setRevision(0);
      }
      if (relocatedToUnassigned) {
        setRestoreStatus(
          "Restored Media was moved to Unassigned Media because its original Moment was removed while restoration was pending. Choose it in Unassigned Media, move it to a Moment, then review the Event before Publication.",
        );
      }
    },
  });
  const restoreConflict =
    restorePublishedMedia.error instanceof APIError &&
    restorePublishedMedia.error.status === 409;

  function resetEventState(nextEventID: string) {
    selectionGenerationRef.current += 1;
    selectedIDRef.current = nextEventID;
    setSelectedID(nextEventID);
    save.reset();
    latestDraftRef.current = undefined;
    localDuringConflict.current = undefined;
    authorityRecoveryAttemptRef.current = undefined;
    setDraft(undefined);
    setInspectedMomentID("");
    revisionRef.current = 0;
    setRevision(0);
    setSaveState("saved");
    setConflictRecoveryError("");
    setConflictRecoveryPending(false);
    setRestoreRecoveryError("");
    setRestoreRecoveryPending(false);
    setRestoreStatus("");
    restorePublishedMedia.reset();
    setPreviewResetGeneration(0);
    setAuthorityRefreshRequired(false);
    setAuthorityRefreshError("");
    setActivePane(nextEventID ? "organize" : "work");
  }

  function ownsAttempt(attempted: EventMutationAttempt) {
    return (
      selectedIDRef.current === attempted.event.id &&
      selectionGenerationRef.current === attempted.selectionGeneration
    );
  }

  const synchronizeURLSelection = useEffectEvent((nextEventID: string) => {
    const previousEventID = selectedIDRef.current;
    if (
      saveState === "saving" ||
      publicationPending ||
      withdrawalPending ||
      restorePublishedMedia.isPending ||
      conflictRecoveryPending ||
      restoreRecoveryPending ||
      authorityRefreshRequired
    ) {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          if (previousEventID) next.set("event", previousEventID);
          else next.delete("event");
          return next;
        },
        { replace: true },
      );
      return;
    }
    if (
      saveState !== "saved" &&
      !window.confirm("Discard changes that have not finished saving?")
    ) {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          if (previousEventID) next.set("event", previousEventID);
          else next.delete("event");
          return next;
        },
        { replace: true },
      );
      return;
    }
    resetEventState(nextEventID);
  });

  useEffect(() => {
    if (!looseKindRequested && requestedEventID !== selectedIDRef.current)
      synchronizeURLSelection(requestedEventID);
  }, [looseKindRequested, requestedEventID]);

  useEffect(() => {
    if (saveState === "saved" && serverDraft)
      latestDraftRef.current = serverDraft;
  }, [saveState, serverDraft]);

  const saveDraft = save.mutate;
  useEffect(() => {
    const dirty = saveState !== "saved";
    const operationPending =
      saveState === "saving" ||
      conflictRecoveryPending ||
      restorePublishedMedia.isPending ||
      restoreRecoveryPending ||
      publicationPending ||
      withdrawalPending ||
      authorityRefreshRequired;
    onDirtyChange?.(dirty);
    onSavingChange?.(operationPending);
    const preventDirtyUnload = (event: BeforeUnloadEvent) => {
      if (!dirty && !operationPending) return;
      event.preventDefault();
    };
    window.addEventListener("beforeunload", preventDirtyUnload);
    return () => window.removeEventListener("beforeunload", preventDirtyUnload);
  }, [
    authorityRefreshRequired,
    conflictRecoveryPending,
    onDirtyChange,
    onSavingChange,
    publicationPending,
    restorePublishedMedia.isPending,
    restoreRecoveryPending,
    saveState,
    withdrawalPending,
  ]);

  useEffect(() => {
    const panes = {
      work: workPaneRef,
      organize: organizePaneRef,
      inspect: inspectPaneRef,
    };
    panes[activePane].current?.focus();
  }, [activePane]);

  useEffect(() => {
    if (
      !currentDraft ||
      revision === 0 ||
      saveState === "conflict" ||
      saveState === "failed" ||
      save.isPending ||
      restorePublishedMedia.isPending ||
      restoreConflict ||
      restoreRecoveryPending ||
      authorityRefreshRequired ||
      publicationPending ||
      withdrawalPending ||
      !metadata.valid
    )
      return;
    const timer = window.setTimeout(
      () =>
        saveDraft({
          event: cloneEvent(currentDraft),
          revision: revisionRef.current,
          selectionGeneration: selectionGenerationRef.current,
        }),
      450,
    );
    return () => window.clearTimeout(timer);
  }, [
    authorityRefreshRequired,
    currentDraft,
    metadata.valid,
    publicationPending,
    restoreConflict,
    restorePublishedMedia.isPending,
    restoreRecoveryPending,
    revision,
    save.isPending,
    saveState,
    saveDraft,
    withdrawalPending,
  ]);

  function change(mutator: (next: DraftEvent) => void) {
    if (!currentDraft) return;
    const next = cloneEvent(currentDraft);
    mutator(next);
    const nextRevision = revisionRef.current + 1;
    revisionRef.current = nextRevision;
    latestDraftRef.current = next;
    setDraft(next);
    setPreviewResetGeneration((current) => current + 1);
    if (saveState !== "failed") setSaveState("unsaved");
    setRevision(nextRevision);
  }

  function reflectReview(
    kind: "attendance-confirmed" | "audience-changed" | "audience-approved",
    momentID: string,
    selectionGeneration: number,
  ) {
    if (
      !currentDraft ||
      selectedIDRef.current !== currentDraft.id ||
      selectionGenerationRef.current !== selectionGeneration
    )
      return;
    const reviewBase = cloneEvent(currentDraft);
    const eventID = reviewBase.id;
    authorityRecoveryAttemptRef.current = undefined;
    setAuthorityRefreshRequired(true);
    setAuthorityRefreshError("");
    const next = cloneEvent(reviewBase);
    const moment = next.moments.find((candidate) => candidate.id === momentID);
    if (moment) {
      if (kind === "attendance-confirmed") {
        moment.attendance_complete = true;
        moment.audience_complete = false;
      } else if (kind === "audience-changed") {
        moment.audience_complete = false;
      } else {
        moment.audience_complete = true;
      }
    }
    next.final_review_complete = false;
    latestDraftRef.current = next;
    setDraft(next);
    setPreviewResetGeneration((current) => current + 1);
    const preserveOrganization = saveState !== "saved";
    void eventQuery.refetchAuthoritative().then((result) => {
      if (
        selectedIDRef.current !== eventID ||
        selectionGenerationRef.current !== selectionGeneration
      )
        return;
      if (!result.isSuccess || !result.data) {
        setAuthorityRefreshError(
          result.error?.message ??
            "The authoritative Event could not be loaded.",
        );
        return;
      }
      const current = result.data;
      const latest = latestDraftRef.current;
      if (preserveOrganization && latest?.id === eventID) {
        const rebased = applyLocalOrganization(latest, current);
        rebased.final_review_complete = false;
        latestDraftRef.current = rebased;
        setDraft(rebased);
        setSaveState(revisionRef.current > 0 ? "unsaved" : "saved");
        setAuthorityRefreshRequired(false);
        return;
      }
      latestDraftRef.current = current;
      setDraft(current);
      setAuthorityRefreshRequired(false);
    });
  }

  async function reloadAndRetryRestoration() {
    const attempted = restorePublishedMedia.variables;
    if (!attempted) return;
    const mediaID = attempted.mediaID;
    setRestoreRecoveryError("");
    setRestoreRecoveryPending(true);
    try {
      const result = await eventQuery.refetchAuthoritative();
      if (!ownsAttempt(attempted)) return;
      if (!result.isSuccess || !result.data) {
        setRestoreRecoveryError(
          result.error?.message ?? "The newer Event could not be loaded.",
        );
        return;
      }
      const authoritative = cloneEvent(result.data);
      const latest = latestDraftRef.current;
      const hasNewerOrganization =
        latest?.id === authoritative.id &&
        revisionRef.current > attempted.revision;
      const next = hasNewerOrganization
        ? rebaseOrganization(attempted.event, latest, authoritative)
        : authoritative;
      latestDraftRef.current = next;
      setDraft(next);
      setSaveState(hasNewerOrganization ? "unsaved" : "saved");
      if (!hasNewerOrganization) {
        revisionRef.current = 0;
        setRevision(0);
      }
      const remainsRestorable = authoritative.staged_update?.changes.some(
        (stagedChange) =>
          stagedChange.removed_media?.some(
            (item) => item.id === mediaID && item.restorable,
          ),
      );
      restorePublishedMedia.reset();
      if (!remainsRestorable) {
        setRestoreRecoveryError(
          "The newer Event no longer offers this restoration. Review its current Staged update.",
        );
        return;
      }
      restorePublishedMedia.mutate({
        event: authoritative,
        mediaID,
        revision: attempted.revision,
        selectionGeneration: attempted.selectionGeneration,
      });
    } finally {
      if (ownsAttempt(attempted)) setRestoreRecoveryPending(false);
    }
  }

  async function loadNewerVersion() {
    const eventID = selectedIDRef.current;
    const selectionGeneration = selectionGenerationRef.current;
    setConflictRecoveryError("");
    setConflictRecoveryPending(true);
    try {
      const result = await eventQuery.refetchAuthoritative();
      if (
        selectedIDRef.current !== eventID ||
        selectionGenerationRef.current !== selectionGeneration
      )
        return;
      if (!result.isSuccess || !result.data) {
        setConflictRecoveryError(
          result.error?.message ?? "The newer Event could not be loaded.",
        );
        return;
      }
      const next = cloneEvent(result.data);
      latestDraftRef.current = next;
      localDuringConflict.current = undefined;
      save.reset();
      setDraft(next);
      revisionRef.current = 0;
      setRevision(0);
      setSaveState("saved");
    } finally {
      if (
        selectedIDRef.current === eventID &&
        selectionGenerationRef.current === selectionGeneration
      )
        setConflictRecoveryPending(false);
    }
  }

  async function keepMyChanges() {
    const eventID = selectedIDRef.current;
    const selectionGeneration = selectionGenerationRef.current;
    setConflictRecoveryError("");
    setConflictRecoveryPending(true);
    try {
      const result = await eventQuery.refetchAuthoritative();
      if (
        selectedIDRef.current !== eventID ||
        selectionGenerationRef.current !== selectionGeneration
      )
        return;
      if (!result.isSuccess || !result.data) {
        setConflictRecoveryError(
          result.error?.message ?? "The newer Event could not be loaded.",
        );
        return;
      }
      const latest = latestDraftRef.current;
      const local =
        latest?.id === eventID ? latest : localDuringConflict.current;
      if (!local) return;
      const next = applyLocalOrganization(local, result.data);
      const nextRevision = revisionRef.current + 1;
      revisionRef.current = nextRevision;
      latestDraftRef.current = next;
      setDraft(next);
      localDuringConflict.current = undefined;
      save.reset();
      setSaveState("unsaved");
      setRevision(nextRevision);
    } finally {
      if (
        selectedIDRef.current === eventID &&
        selectionGenerationRef.current === selectionGeneration
      )
        setConflictRecoveryPending(false);
    }
  }

  function reconcileAuthoritative(
    attempted: EventMutationAttempt,
    server: DraftEvent,
  ) {
    if (!ownsAttempt(attempted)) return;
    const latest = latestDraftRef.current;
    const hasNewerOrganization = revisionRef.current > attempted.revision;
    const next =
      latest?.id === server.id
        ? rebaseOrganization(attempted.event, latest, server)
        : cloneEvent(server);
    latestDraftRef.current = next;
    setDraft(next);
    setSaveState(hasNewerOrganization ? "unsaved" : "saved");
    if (!hasNewerOrganization) {
      revisionRef.current = 0;
      setRevision(0);
    }
  }

  function handlePublished(
    _publication: PublicationResponse,
    attempted: PublishEventAttempt,
    server: DraftEvent | undefined,
  ) {
    if (!ownsAttempt(attempted)) return;
    if (server) {
      authorityRecoveryAttemptRef.current = undefined;
      setAuthorityRefreshRequired(false);
      setAuthorityRefreshError("");
      reconcileAuthoritative(attempted, server);
      return;
    }
    authorityRecoveryAttemptRef.current = attempted;
    void recoverAuthoritativeEvent(attempted);
  }

  async function recoverAuthoritativeEvent(
    attempted = authorityRecoveryAttemptRef.current,
  ) {
    const eventID = attempted?.event.id ?? selectedIDRef.current;
    const selectionGeneration =
      attempted?.selectionGeneration ?? selectionGenerationRef.current;
    if (
      !eventID ||
      selectedIDRef.current !== eventID ||
      selectionGenerationRef.current !== selectionGeneration
    )
      return;
    setAuthorityRefreshRequired(true);
    setAuthorityRefreshError("");
    const result = await eventQuery.refetchAuthoritative();
    if (
      selectedIDRef.current !== eventID ||
      selectionGenerationRef.current !== selectionGeneration
    )
      return;
    if (!result.isSuccess || !result.data) {
      setAuthorityRefreshError(
        result.error?.message ?? "The authoritative Event could not be loaded.",
      );
      return;
    }
    if (attempted) reconcileAuthoritative(attempted, result.data);
    else {
      const latest = latestDraftRef.current;
      if (latest?.id === eventID && revisionRef.current > 0) {
        const rebased = applyLocalOrganization(latest, result.data);
        latestDraftRef.current = rebased;
        setDraft(rebased);
        setSaveState("unsaved");
      } else {
        latestDraftRef.current = result.data;
        setDraft(undefined);
        revisionRef.current = 0;
        setRevision(0);
        setSaveState("saved");
      }
    }
    authorityRecoveryAttemptRef.current = undefined;
    setAuthorityRefreshRequired(false);
  }

  function handleAccessMutationStarted() {
    setPreviewResetGeneration((current) => current + 1);
  }

  function handleAuthorityUncertain(attempted: EventMutationAttempt) {
    setPreviewResetGeneration((current) => current + 1);
    if (!ownsAttempt(attempted)) return;
    authorityRecoveryAttemptRef.current = attempted;
    void recoverAuthoritativeEvent(attempted);
  }

  function handleWithdrawalCommitted(attempted: WithdrawEventAttempt) {
    if (!ownsAttempt(attempted)) return;
    authorityRecoveryAttemptRef.current = attempted;
    setAuthorityRefreshRequired(true);
    setAuthorityRefreshError("");
  }

  function handleWithdrawn(
    _withdrawal: WithdrawalResult,
    attempted: WithdrawEventAttempt,
    server: DraftEvent | undefined,
  ) {
    if (!ownsAttempt(attempted)) return;
    if (server) {
      authorityRecoveryAttemptRef.current = undefined;
      setAuthorityRefreshRequired(false);
      setAuthorityRefreshError("");
      reconcileAuthoritative(attempted, server);
      return;
    }
    void recoverAuthoritativeEvent(attempted);
  }

  const inspected = currentDraft?.moments.find(
    (moment) => moment.id === inspectedMomentID,
  );
  const allMediaCount = currentDraft
    ? currentDraft.moments.reduce(
        (count, moment) => count + moment.media_items.length,
        currentDraft.unassigned_media.length,
      )
    : 0;
  const moduleResetKey = `${selectedID}:${currentDraft?.version ?? ""}:${revision}:${previewResetGeneration}`;
  const accessControlsReady =
    saveState === "saved" &&
    !authorityRefreshRequired &&
    !publicationPending &&
    !withdrawalPending;

  return (
    <section aria-labelledby="curator-work-title" className="curator-workspace">
      <header className="work-header">
        <div>
          <p className="step-label">Curator workspace</p>
          <h2 id="curator-work-title">Organize drafts</h2>
        </div>
        <p aria-live="polite" className={`save-state ${saveState}`}>
          {saveState === "conflict"
            ? "Save conflict"
            : !metadata.valid
              ? "Fix validation errors before autosave"
              : saveState === "saved"
                ? "All changes saved"
                : saveState === "saving"
                  ? "Saving…"
                  : saveState === "failed"
                    ? "Autosave failed"
                    : "Changes not saved yet"}
        </p>
      </header>
      {restoreStatus ? (
        <p aria-live="polite" role="status">
          {restoreStatus}
        </p>
      ) : null}
      {authorityRefreshRequired ? (
        <div className="form-error" role="alert">
          <p>
            Reload the authoritative Event before Preview, Withdrawal, or
            Publication can continue.
          </p>
          {authorityRefreshError ? <p>{authorityRefreshError}</p> : null}
          <button
            disabled={!authorityRefreshError}
            onClick={() => void recoverAuthoritativeEvent()}
            type="button"
          >
            {authorityRefreshError ? "Retry Event reload" : "Reloading Event…"}
          </button>
        </div>
      ) : null}
      <nav aria-label="Mobile workspace panes" className="mobile-pane-nav">
        {(["work", "organize", "inspect"] as Pane[]).map((pane) => (
          <button
            aria-controls={`${pane}-pane`}
            aria-pressed={activePane === pane}
            key={pane}
            onClick={() => setActivePane(pane)}
            type="button"
          >
            {pane === "work"
              ? "Work"
              : pane === "organize"
                ? "Event"
                : "Inspect"}
          </button>
        ))}
      </nav>
      {saveState === "conflict" ? (
        <div className="conflict" role="alert">
          <strong>This Event changed in another browser.</strong>
          <p>Your edits have not overwritten the newer version.</p>
          <p>
            Replacing it will discard organization saved by the other browser.
          </p>
          {conflictRecoveryError ? (
            <p className="form-error">{conflictRecoveryError}</p>
          ) : null}
          <button
            disabled={conflictRecoveryPending}
            onClick={() => void loadNewerVersion()}
            type="button"
          >
            Load newer version
          </button>
          <button
            disabled={conflictRecoveryPending || !metadata.valid}
            onClick={() => void keepMyChanges()}
            type="button"
          >
            Replace newer version with my changes
          </button>
        </div>
      ) : save.isError ? (
        <div className="form-error" role="alert">
          <p>{save.error.message}</p>
          <button
            disabled={!currentDraft || save.isPending || !metadata.valid}
            onClick={() => {
              if (currentDraft)
                save.mutate({
                  event: cloneEvent(currentDraft),
                  revision: revisionRef.current,
                  selectionGeneration: selectionGenerationRef.current,
                });
            }}
            type="button"
          >
            Retry autosave
          </button>
        </div>
      ) : null}
      <fieldset
        aria-label="Curator Event workspace"
        className="curator-split"
        data-active-pane={activePane}
        disabled={conflictRecoveryPending || authorityRefreshRequired}
      >
        <aside
          className="work-pane"
          id="work-pane"
          ref={workPaneRef}
          tabIndex={-1}
        >
          <h3>Event work</h3>
          {work.isPending ? <p>Loading Events…</p> : null}
          {work.isError ? (
            <p className="form-error" role="alert">
              {work.error.message}
            </p>
          ) : null}
          {work.data?.events.length === 0 ? <p>No Events yet.</p> : null}
          <ul className="event-list">
            {work.data?.events.map((event) => (
              <li key={event.id}>
                <button
                  aria-current={selectedID === event.id ? "page" : undefined}
                  disabled={
                    event.id !== selectedID &&
                    (save.isPending ||
                      publicationPending ||
                      withdrawalPending ||
                      restorePublishedMedia.isPending)
                  }
                  onClick={() => {
                    if (event.id === selectedID) return;
                    if (
                      saveState !== "saved" &&
                      !window.confirm(
                        "Discard changes that have not finished saving?",
                      )
                    )
                      return;
                    resetEventState(event.id);
                    setSearchParams(
                      (current) => {
                        const next = new URLSearchParams(current);
                        next.set("event", event.id);
                        return next;
                      },
                      { replace: true },
                    );
                  }}
                  type="button"
                >
                  <strong>{event.title}</strong>
                  <span>
                    {event.has_staged_update
                      ? "Staged update"
                      : event.lifecycle === "published"
                        ? "Published"
                        : "Draft"}{" "}
                    · {event.moment_count} Moments · {event.unassigned_count}{" "}
                    unassigned
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <section
          aria-label="Active Event organization"
          className="organize-pane"
          id="organize-pane"
          ref={organizePaneRef}
          tabIndex={-1}
        >
          {!selectedID ? (
            <p className="pane-empty">Choose an Event from Work.</p>
          ) : null}
          {eventQuery.isPending && selectedID ? <p>Loading Event…</p> : null}
          {eventQuery.isError && selectedID && saveState !== "conflict" ? (
            <div className="form-error" role="alert">
              <p>{eventQuery.error.message}</p>
              <button
                onClick={() => void eventQuery.refetchAuthoritative()}
                type="button"
              >
                Retry loading Event
              </button>
            </div>
          ) : null}
          {currentDraft ? (
            <EventOrganization
              event={currentDraft}
              hasUnsavedChanges={saveState !== "saved"}
              inspectionDisabled={saveState !== "saved"}
              key={currentDraft.id}
              metadataValid={metadata.valid}
              onChange={change}
              onInspectionTargetChange={(momentID, openPane) => {
                setInspectedMomentID(momentID);
                if (openPane) setActivePane("inspect");
              }}
              coverValidationError={metadata.coverError}
              dateValidationError={metadata.dateError}
              stagedReview={
                <StagedUpdateReview
                  event={currentDraft}
                  metadataValid={metadata.valid}
                  onRecoverRestore={() => void reloadAndRetryRestoration()}
                  onRestoreMedia={(mediaID) =>
                    restorePublishedMedia.mutate({
                      event: currentDraft,
                      mediaID,
                      revision: revisionRef.current,
                      selectionGeneration: selectionGenerationRef.current,
                    })
                  }
                  restoreConflict={restoreConflict}
                  restoreDisabled={saveState !== "saved"}
                  restoreError={
                    restoreRecoveryError ||
                    (restorePublishedMedia.error instanceof APIError &&
                    restorePublishedMedia.error.status === 409
                      ? undefined
                      : restorePublishedMedia.error?.message)
                  }
                  restoreRecoveryPending={restoreRecoveryPending}
                  restoringMediaID={
                    restorePublishedMedia.isPending
                      ? restorePublishedMedia.variables?.mediaID
                      : undefined
                  }
                />
              }
              timezoneValidationError={metadata.timezoneError}
              titleValidationError={metadata.titleError}
            />
          ) : null}
        </section>
        <aside
          className="inspect-pane"
          id="inspect-pane"
          ref={inspectPaneRef}
          tabIndex={-1}
        >
          {currentDraft ? (
            <>
              <AudienceInspection
                event={currentDraft}
                identityGeneration={identityGeneration}
                inspectedMoment={inspected}
                onEventChange={change}
                onReviewChanged={reflectReview}
                selectionGeneration={selectionGenerationRef.current}
              />
              <Withdrawal
                event={currentDraft}
                identityGeneration={identityGeneration}
                onAuthorityUncertain={handleAuthorityUncertain}
                onBusyChange={setWithdrawalPending}
                onCommitted={handleWithdrawalCommitted}
                onStarted={handleAccessMutationStarted}
                onWithdrawn={handleWithdrawn}
                resetKey={selectedID}
                revision={revision}
                selectionGeneration={selectionGenerationRef.current}
                saveIsSaved={accessControlsReady}
              />
              <Publication
                event={currentDraft}
                identityGeneration={identityGeneration}
                metadataValid={metadata.valid}
                onAuthorityUncertain={handleAuthorityUncertain}
                onBusyChange={setPublicationPending}
                onPublished={handlePublished}
                previewResetKey={moduleResetKey}
                resetKey={selectedID}
                revision={revision}
                selectionGeneration={selectionGenerationRef.current}
                saveIsSaved={accessControlsReady}
              />
            </>
          ) : (
            <>
              <h3>Attendance and Audience</h3>
              <p>Choose a Moment to inspect.</p>
            </>
          )}
          <p className="visually-hidden">{allMediaCount} total Media items</p>
        </aside>
      </fieldset>
    </section>
  );
}
