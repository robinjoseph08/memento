import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";

import { APIError, apiJSON } from "./api";
import { AttendanceAudienceReview } from "./AttendanceAudienceReview";
import type {
  Event as DraftEvent,
  EventListResponse,
  MediaItem,
  Moment,
  OrganizeEventRequest,
  PreviewRecipientsResponse,
  PublicationResponse,
  PublishedEventView,
  Withdrawal,
  WithdrawalTarget,
} from "./types/generated/events";
import type { SessionResponse } from "./types/generated/setup";

type Pane = "work" | "organize" | "inspect";
type SaveState = "saved" | "saving" | "unsaved" | "failed" | "conflict";
type SaveAttempt = { event: DraftEvent; revision: number };

function mediaLabel(item: Pick<MediaItem, "media_type" | "local_date_time">) {
  if (!item.local_date_time) return `Undated ${item.media_type}`;
  const parsed = new Date(item.local_date_time);
  return Number.isNaN(parsed.valueOf())
    ? `Undated ${item.media_type}`
    : `${item.media_type}, ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed)}`;
}

function cloneEvent(event: DraftEvent): DraftEvent {
  return structuredClone(event);
}

const maxPlaceLabels = 20;
const maxPlaceLabelLength = 120;

function parsePlaceLabels(value: string) {
  return value
    .split(",")
    .map((label) => label.trim())
    .filter(Boolean);
}

function validatePlaceLabels(labels: string[]) {
  if (labels.length > maxPlaceLabels)
    return `Use no more than ${maxPlaceLabels} Place labels.`;
  if (labels.some((label) => Array.from(label).length > maxPlaceLabelLength))
    return `Each Place label must be ${maxPlaceLabelLength} characters or fewer.`;
  return "";
}

function mergePlaceLabels(...groups: string[][]) {
  const labels: string[] = [];
  const seen = new Set<string>();
  for (const value of groups.flat()) {
    const label = value.trim();
    const key = label.toLowerCase();
    if (!label || seen.has(key)) continue;
    seen.add(key);
    labels.push(label);
  }
  return labels;
}

function PlaceLabelEditor({
  ariaLabel,
  labels,
  onCommit,
  placeholder,
}: {
  ariaLabel: string;
  labels: string[];
  onCommit: (labels: string[]) => void;
  placeholder: string;
}) {
  const [raw, setRaw] = useState(labels.join(", "));
  const [error, setError] = useState("");
  const focused = useRef(false);

  useEffect(() => {
    if (!focused.current) setRaw(labels.join(", "));
  }, [labels]);

  return (
    <label className="place-label-editor">
      {ariaLabel}
      <input
        aria-label={ariaLabel}
        aria-invalid={error ? "true" : undefined}
        onBlur={() => {
          focused.current = false;
          const parsed = parsePlaceLabels(raw);
          const validationError = validatePlaceLabels(parsed);
          setError(validationError);
          if (validationError) return;
          setRaw(parsed.join(", "));
          if (
            parsed.length !== labels.length ||
            parsed.some((label, index) => label !== labels[index])
          )
            onCommit(parsed);
        }}
        onChange={(event) => {
          setRaw(event.target.value);
          setError("");
        }}
        onFocus={() => {
          focused.current = true;
        }}
        placeholder={placeholder}
        value={raw}
      />
      <span>
        Up to {maxPlaceLabels} comma-separated labels, {maxPlaceLabelLength}{" "}
        characters each. Labels become searchable after Publication.
      </span>
      {error ? (
        <span className="form-error" role="alert">
          {error}
        </span>
      ) : null}
    </label>
  );
}

function organizationRequest(event: DraftEvent): OrganizeEventRequest {
  return {
    version: event.version,
    place_labels: event.place_labels,
    moments: event.moments.map((moment) => ({
      id: moment.id,
      title: moment.title,
      place_labels: moment.place_labels,
      proposed_day: moment.proposed_day,
      cover_media_item_id: moment.cover_media_item_id,
      media_item_ids: moment.media_items.map((item) => item.id),
    })),
    unassigned_media_ids: event.unassigned_media.map((item) => item.id),
    final_review_complete: event.final_review_complete,
  };
}

const stagedLabels: Record<string, string> = {
  addition: "Additions",
  removal: "Removals",
  move: "Moves and ordering",
  metadata: "Metadata edits",
  moment_structure: "Moment structure",
  access: "Access changes",
};

function StagedUpdateReview({ event }: { event: DraftEvent }) {
  if (!event.staged_update) return null;
  const removedMedia = event.staged_update.changes.flatMap(
    (change) => change.removed_media ?? [],
  );
  const deletedMoments = event.staged_update.changes.flatMap(
    (change) => change.deleted_moments ?? [],
  );
  return (
    <section aria-labelledby="staged-review-title" className="staged-review">
      <div>
        <p className="step-label">Private until Publication</p>
        <h4 id="staged-review-title">Staged update review</h4>
        <p>The organization below is the complete resulting Event.</p>
        {removedMedia.length > 0 || deletedMoments.length > 0 ? (
          <section
            aria-labelledby="removed-items-title"
            className="staged-removed"
          >
            <h5 id="removed-items-title">Removed from the resulting Event</h5>
            <ul>
              {removedMedia.map((item) => (
                <li className="staged-removal" key={`media-${item.id}`}>
                  <strong>Removed Media</strong>
                  <span>{mediaLabel(item)}</span>
                  <code>{item.id}</code>
                </li>
              ))}
              {deletedMoments.map((moment) => (
                <li className="staged-removal" key={`moment-${moment.id}`}>
                  <strong>Deleted Moment</strong>
                  <span>{moment.title || moment.proposed_day}</span>
                  <code>{moment.id}</code>
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </div>
      <ul aria-label="Net change summary">
        {event.staged_update.changes.map((change) => (
          <li key={change.kind}>
            <strong>{stagedLabels[change.kind] ?? change.kind}</strong>
            <span>{change.count}</span>
            <small>{change.detail}</small>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Checklist({ event }: { event: DraftEvent }) {
  const checks = [
    { label: "Media organization", done: event.unassigned_media.length === 0 },
    {
      label: "Moments",
      done:
        event.moments.length > 0 &&
        event.moments.every(
          (moment) =>
            moment.media_items.length > 0 &&
            moment.cover_media_item_id !== null,
        ),
    },
    {
      label: "Attendance",
      done:
        event.moments.length > 0 &&
        event.moments.every((moment) => moment.attendance_complete),
    },
    {
      label: "Audiences",
      done:
        event.moments.length > 0 &&
        event.moments.every((moment) => moment.audience_complete),
    },
    { label: "Final review", done: event.final_review_complete },
  ];
  const complete = checks.filter((check) => check.done).length;
  const next = checks.find((check) => !check.done)?.label ?? "Ready to publish";
  return (
    <section aria-labelledby="readiness-title" className="readiness">
      <h3 id="readiness-title">Readiness</h3>
      <p>
        {complete} of {checks.length} complete
      </p>
      <progress
        aria-label="Draft progress"
        max={checks.length}
        value={complete}
      />
      <ul>
        {checks.map((check) => (
          <li key={check.label}>
            <span aria-hidden="true">{check.done ? "✓" : "○"}</span>{" "}
            {check.label}
          </li>
        ))}
      </ul>
      <p>
        <strong>Next action:</strong> {next}
      </p>
    </section>
  );
}

function MediaRow({
  item,
  selected,
  onSelect,
  onMove,
  stagedKind,
}: {
  item: MediaItem;
  selected: boolean;
  onSelect: () => void;
  onMove: (direction: -1 | 1) => void;
  stagedKind?: string;
}) {
  return (
    <li
      className={`media-row${stagedKind ? ` staged-${stagedKind}` : ""}`}
      onKeyDown={(event) => {
        if (event.altKey && event.key === "ArrowUp") {
          event.preventDefault();
          onMove(-1);
        }
        if (event.altKey && event.key === "ArrowDown") {
          event.preventDefault();
          onMove(1);
        }
      }}
    >
      <label>
        <input checked={selected} onChange={onSelect} type="checkbox" />
        <span>{mediaLabel(item)}</span>
      </label>
      <span className="row-actions">
        <button
          aria-label={`Move ${mediaLabel(item)} earlier`}
          onClick={() => onMove(-1)}
          type="button"
        >
          ↑
        </button>
        <button
          aria-label={`Move ${mediaLabel(item)} later`}
          onClick={() => onMove(1)}
          type="button"
        >
          ↓
        </button>
      </span>
    </li>
  );
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
  const queryClient = useQueryClient();
  const [selectedID, setSelectedID] = useState("");
  const [draft, setDraft] = useState<DraftEvent>();
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [destination, setDestination] = useState("unassigned");
  const [newMomentDay, setNewMomentDay] = useState("");
  const [inspectedMomentID, setInspectedMomentID] = useState("");
  const [activePane, setActivePane] = useState<Pane>("work");
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [conflictRecoveryError, setConflictRecoveryError] = useState("");
  const [conflictRecoveryPending, setConflictRecoveryPending] = useState(false);
  const [mergeError, setMergeError] = useState("");
  const [revision, setRevision] = useState(0);
  const [notifyRecipients, setNotifyRecipients] = useState(true);
  const [previewRecipientID, setPreviewRecipientID] = useState("");
  const [previewOpen, setPreviewOpen] = useState(false);
  const [withdrawTarget, setWithdrawTarget] = useState<WithdrawalTarget>();
  const [withdrawReason, setWithdrawReason] = useState("");
  const revisionRef = useRef(0);
  const latestDraftRef = useRef<DraftEvent | undefined>(undefined);
  const selectedIDRef = useRef("");
  const localDuringConflict = useRef<DraftEvent | undefined>(undefined);
  const workPaneRef = useRef<HTMLElement>(null);
  const organizePaneRef = useRef<HTMLElement>(null);
  const inspectPaneRef = useRef<HTMLElement>(null);

  const work = useQuery({
    queryKey: ["events"],
    queryFn: () => apiJSON<EventListResponse>("/api/events"),
    retry: false,
  });
  const eventQuery = useQuery({
    queryKey: ["event", selectedID],
    queryFn: () => apiJSON<DraftEvent>(`/api/events/${selectedID}`),
    enabled: selectedID.length > 0,
    retry: false,
  });

  const currentDraft =
    draft?.id === selectedID
      ? draft
      : eventQuery.data?.id === selectedID
        ? eventQuery.data
        : undefined;
  const stagedMediaKinds = useMemo(() => {
    const kinds = new Map<string, string>();
    for (const change of currentDraft?.staged_update?.changes ?? []) {
      for (const mediaID of change.media_item_ids)
        kinds.set(mediaID, change.kind);
    }
    return kinds;
  }, [currentDraft?.staged_update]);
  const stagedMomentKinds = useMemo(() => {
    const kinds = new Map<string, string[]>();
    for (const change of currentDraft?.staged_update?.changes ?? []) {
      for (const momentID of change.moment_ids) {
        kinds.set(momentID, [...(kinds.get(momentID) ?? []), change.kind]);
      }
    }
    return kinds;
  }, [currentDraft?.staged_update]);

  const previewRecipients = useQuery({
    queryKey: ["preview-recipients", selectedID],
    queryFn: () =>
      apiJSON<PreviewRecipientsResponse>(
        `/api/events/${selectedID}/preview-recipients`,
      ),
    enabled:
      selectedID.length > 0 &&
      currentDraft !== undefined &&
      currentDraft.moments.length > 0 &&
      currentDraft.moments.every((moment) => moment.audience_complete),
    retry: false,
  });
  const preview = useQuery({
    queryKey: [
      "event-preview",
      selectedID,
      currentDraft?.version,
      previewRecipientID,
    ],
    queryFn: () =>
      apiJSON<PublishedEventView>(
        `/api/events/${selectedID}/preview?recipient_person_id=${encodeURIComponent(previewRecipientID)}`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
        },
      ),
    enabled:
      previewOpen && selectedID.length > 0 && previewRecipientID.length > 0,
    retry: false,
  });
  const publish = useMutation({
    mutationFn: (event: DraftEvent) =>
      apiJSON<PublicationResponse>(`/api/events/${event.id}/publications`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({
          version: event.version,
          notify_recipients: notifyRecipients,
        }),
      }),
    onSuccess: (publication, publishedEvent) => {
      setPreviewOpen(false);
      setDraft((current) =>
        current?.id === publishedEvent.id
          ? {
              ...current,
              lifecycle: "published",
              published_editable_version: publication.editable_version,
              published_attendance_recovery_required: false,
              staged_update: null,
            }
          : current,
      );
      void queryClient.invalidateQueries({
        queryKey: ["event", publishedEvent.id],
      });
      void queryClient.invalidateQueries({ queryKey: ["events"] });
    },
  });

  const withdraw = useMutation({
    mutationFn: (target: WithdrawalTarget) =>
      apiJSON<Withdrawal>("/api/withdrawals", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({
          target_kind: target.target_kind,
          target_id: target.target_id,
          reason: withdrawReason,
        }),
      }),
    onSuccess: () => {
      setWithdrawTarget(undefined);
      setWithdrawReason("");
      setPreviewOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["events"] });
      void queryClient.invalidateQueries({ queryKey: ["event"] });
      void eventQuery.refetch().then((result) => {
        if (!result.data) return;
        latestDraftRef.current = result.data;
        setDraft(result.data);
        setRevision(0);
        setSaveState("saved");
      });
    },
  });

  const save = useMutation({
    mutationFn: ({ event }: SaveAttempt) =>
      apiJSON<DraftEvent>(`/api/events/${event.id}/organization`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(organizationRequest(event)),
      }),
    onMutate: () => setSaveState("saving"),
    onSuccess: (saved, attempted) => {
      queryClient.setQueryData(["event", saved.id], saved);
      void queryClient.invalidateQueries({ queryKey: ["events"] });
      void queryClient.invalidateQueries({
        queryKey: ["attendance-audience"],
      });
      if (selectedIDRef.current !== saved.id) return;

      const latest = latestDraftRef.current;
      if (latest?.id === saved.id && revisionRef.current > attempted.revision) {
        const rebased = cloneEvent(latest);
        rebased.version = saved.version;
        latestDraftRef.current = rebased;
        setDraft(rebased);
        setSaveState("unsaved");
        return;
      }

      const next = cloneEvent(saved);
      latestDraftRef.current = next;
      setDraft(next);
      setSaveState("saved");
      setRevision(0);
    },
    onError: (error, attempted) => {
      if (selectedIDRef.current !== attempted.event.id) return;
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

  const saveDraft = save.mutate;
  useEffect(() => {
    const dirty = saveState !== "saved";
    onDirtyChange?.(dirty);
    onSavingChange?.(saveState === "saving" || conflictRecoveryPending);
    const preventDirtyUnload = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
    };
    window.addEventListener("beforeunload", preventDirtyUnload);
    return () => window.removeEventListener("beforeunload", preventDirtyUnload);
  }, [conflictRecoveryPending, onDirtyChange, onSavingChange, saveState]);

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
      save.isPending
    )
      return;
    const timer = window.setTimeout(
      () =>
        saveDraft({
          event: cloneEvent(currentDraft),
          revision: revisionRef.current,
        }),
      450,
    );
    return () => window.clearTimeout(timer);
  }, [currentDraft, revision, saveState, save.isPending, saveDraft]);

  function change(mutator: (next: DraftEvent) => void) {
    if (!currentDraft) return;
    setPreviewOpen(false);
    const next = cloneEvent(currentDraft);
    mutator(next);
    const nextRevision = revisionRef.current + 1;
    revisionRef.current = nextRevision;
    latestDraftRef.current = next;
    setDraft(next);
    setSaveState("unsaved");
    setRevision(nextRevision);
  }

  function reflectReview(mutator: (next: DraftEvent) => void) {
    if (!currentDraft) return;
    setPreviewOpen(false);
    const next = cloneEvent(currentDraft);
    mutator(next);
    next.final_review_complete = false;
    latestDraftRef.current = next;
    queryClient.setQueryData(["event", next.id], next);
    setDraft(next);
    const eventID = next.id;
    const localRevision = revisionRef.current;
    const preserveOrganization = saveState !== "saved";
    void apiJSON<DraftEvent>(`/api/events/${eventID}`)
      .then((current) => {
        if (
          selectedIDRef.current !== eventID ||
          revisionRef.current !== localRevision
        ) {
          queryClient.setQueryData(["event", eventID], current);
          return;
        }
        const latest = latestDraftRef.current;
        if (preserveOrganization && latest?.id === eventID) {
          const reviewByMoment = new Map(
            current.moments.map((moment) => [moment.id, moment]),
          );
          const rebased = cloneEvent(latest);
          rebased.version = current.version;
          rebased.lifecycle = current.lifecycle;
          rebased.final_review_complete = false;
          rebased.published_editable_version =
            current.published_editable_version;
          rebased.moments = rebased.moments.map((moment) => {
            const review = reviewByMoment.get(moment.id);
            return review
              ? {
                  ...moment,
                  attendance_complete: review.attendance_complete,
                  audience_complete: review.audience_complete,
                }
              : moment;
          });
          latestDraftRef.current = rebased;
          queryClient.setQueryData(["event", eventID], rebased);
          setDraft(rebased);
          setSaveState("unsaved");
          return;
        }
        latestDraftRef.current = current;
        queryClient.setQueryData(["event", eventID], current);
        setDraft(current);
      })
      .catch(() =>
        queryClient.invalidateQueries({ queryKey: ["event", eventID] }),
      );
  }

  function locateMedia(event: DraftEvent, id: string) {
    for (const moment of event.moments) {
      const index = moment.media_items.findIndex((item) => item.id === id);
      if (index >= 0) return { items: moment.media_items, index };
    }
    const index = event.unassigned_media.findIndex((item) => item.id === id);
    return { items: event.unassigned_media, index };
  }

  function reorderMedia(id: string, direction: -1 | 1) {
    change((next) => {
      const located = locateMedia(next, id);
      const target = located.index + direction;
      if (located.index < 0 || target < 0 || target >= located.items.length)
        return;
      [located.items[located.index], located.items[target]] = [
        located.items[target],
        located.items[located.index],
      ];
    });
  }

  function takeSelectedMedia(event: DraftEvent) {
    const moving: MediaItem[] = [];
    const takeFrom = (items: MediaItem[]) =>
      items.filter((item) => {
        if (!selectedMedia.has(item.id)) return true;
        moving.push(item);
        return false;
      });
    event.unassigned_media = takeFrom(event.unassigned_media);
    for (const moment of event.moments)
      moment.media_items = takeFrom(moment.media_items);
    return moving;
  }

  function moveSelected(targetID = destination) {
    if (
      selectedMedia.size === 0 ||
      (targetID !== "unassigned" &&
        !currentDraft?.moments.some((moment) => moment.id === targetID))
    )
      return;
    change((next) => {
      const moving = takeSelectedMedia(next);
      if (targetID === "unassigned") next.unassigned_media.push(...moving);
      else
        next.moments
          .find((moment) => moment.id === targetID)!
          .media_items.push(...moving);
      next.moments = next.moments.filter(
        (moment) => moment.media_items.length > 0,
      );
      for (const moment of next.moments) {
        if (
          moment.cover_media_item_id &&
          !moment.media_items.some(
            (item) => item.id === moment.cover_media_item_id,
          )
        ) {
          moment.cover_media_item_id = null;
        }
      }
    });
    setSelectedMedia(new Set());
  }

  function createMomentFromSelected() {
    if (!currentDraft || selectedMedia.size === 0 || !newMomentDay) return;
    const id = crypto.randomUUID();
    change((next) => {
      const moving = takeSelectedMedia(next);
      next.moments = next.moments.filter(
        (moment) => moment.media_items.length > 0,
      );
      for (const moment of next.moments) {
        if (
          moment.cover_media_item_id &&
          !moment.media_items.some(
            (item) => item.id === moment.cover_media_item_id,
          )
        ) {
          moment.cover_media_item_id = null;
        }
      }
      next.moments.push({
        id,
        title: "",
        place_labels: [],
        proposed_day: newMomentDay,
        grouping_timezone: next.grouping_timezone,
        source_days: [],
        proposal_kind: "manual",
        cover_media_item_id: null,
        attendance_complete: false,
        audience_complete: false,
        media_items: moving,
      });
    });
    setSelectedMedia(new Set());
    setDestination(id);
    setInspectedMomentID(id);
  }

  function splitMoment(moment: Moment) {
    const chosen = moment.media_items.filter((item) =>
      selectedMedia.has(item.id),
    );
    if (chosen.length === 0 || chosen.length === moment.media_items.length)
      return;
    const id = crypto.randomUUID();
    change((next) => {
      const index = next.moments.findIndex(
        (candidate) => candidate.id === moment.id,
      );
      const source = next.moments[index];
      source.media_items = source.media_items.filter(
        (item) => !selectedMedia.has(item.id),
      );
      if (
        source.cover_media_item_id &&
        selectedMedia.has(source.cover_media_item_id)
      )
        source.cover_media_item_id = null;
      next.moments.splice(index + 1, 0, {
        id,
        title: "",
        place_labels: [...source.place_labels],
        proposed_day: source.proposed_day,
        grouping_timezone: source.grouping_timezone,
        source_days: source.source_days,
        proposal_kind: "split_day",
        cover_media_item_id: null,
        attendance_complete: false,
        audience_complete: false,
        media_items: chosen,
      });
    });
    setSelectedMedia(new Set());
    setInspectedMomentID(id);
  }

  function mergeWithPrevious(index: number) {
    if (!currentDraft || index < 1) return;
    const previousMoment = currentDraft.moments[index - 1];
    const removedMoment = currentDraft.moments[index];
    const placeLabels = mergePlaceLabels(
      previousMoment.place_labels,
      removedMoment.place_labels,
    );
    const validationError = validatePlaceLabels(placeLabels);
    if (validationError) {
      setMergeError(
        `${validationError} Remove Place labels before merging these Moments.`,
      );
      return;
    }
    setMergeError("");
    change((next) => {
      const previous = next.moments[index - 1];
      const removed = next.moments[index];
      previous.place_labels = placeLabels;
      previous.media_items.push(...removed.media_items);
      next.moments.splice(index, 1);
    });
    if (destination === removedMoment.id) setDestination(previousMoment.id);
    setInspectedMomentID(previousMoment.id);
  }

  function reorderMoment(index: number, direction: -1 | 1) {
    change((next) => {
      const target = index + direction;
      if (target < 0 || target >= next.moments.length) return;
      [next.moments[index], next.moments[target]] = [
        next.moments[target],
        next.moments[index],
      ];
    });
  }

  async function loadNewerVersion() {
    setConflictRecoveryError("");
    setConflictRecoveryPending(true);
    try {
      const result = await eventQuery.refetch();
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
      setRevision(0);
      setSaveState("saved");
    } finally {
      setConflictRecoveryPending(false);
    }
  }

  async function keepMyChanges() {
    setConflictRecoveryError("");
    setConflictRecoveryPending(true);
    try {
      const result = await eventQuery.refetch();
      if (!result.isSuccess || !result.data) {
        setConflictRecoveryError(
          result.error?.message ?? "The newer Event could not be loaded.",
        );
        return;
      }
      const latest = latestDraftRef.current;
      const local =
        latest?.id === selectedID ? latest : localDuringConflict.current;
      if (!local) return;
      const next = cloneEvent(local);
      next.version = result.data.version;
      const nextRevision = revisionRef.current + 1;
      revisionRef.current = nextRevision;
      latestDraftRef.current = next;
      setDraft(next);
      localDuringConflict.current = undefined;
      save.reset();
      setSaveState("unsaved");
      setRevision(nextRevision);
    } finally {
      setConflictRecoveryPending(false);
    }
  }

  const inspected = currentDraft?.moments.find(
    (moment) => moment.id === inspectedMomentID,
  );
  const allMedia = useMemo(
    () =>
      currentDraft
        ? [
            ...currentDraft.moments.flatMap((moment) => moment.media_items),
            ...currentDraft.unassigned_media,
          ]
        : [],
    [currentDraft],
  );
  const withdrawalTargets = currentDraft?.withdrawal_targets ?? [];
  const selectedWithdrawTarget =
    withdrawTarget &&
    withdrawalTargets.some(
      (target) =>
        target.target_kind === withdrawTarget.target_kind &&
        target.target_id === withdrawTarget.target_id,
    )
      ? withdrawTarget
      : withdrawalTargets[0];

  return (
    <section aria-labelledby="curator-work-title" className="curator-workspace">
      <header className="work-header">
        <div>
          <p className="step-label">Curator workspace</p>
          <h2 id="curator-work-title">Organize drafts</h2>
        </div>
        <p aria-live="polite" className={`save-state ${saveState}`}>
          {saveState === "saved"
            ? "All changes saved"
            : saveState === "saving"
              ? "Saving…"
              : saveState === "conflict"
                ? "Save conflict"
                : saveState === "failed"
                  ? "Autosave failed"
                  : "Changes not saved yet"}
        </p>
      </header>
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
            disabled={conflictRecoveryPending}
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
            disabled={!currentDraft || save.isPending}
            onClick={() => {
              if (currentDraft)
                save.mutate({
                  event: cloneEvent(currentDraft),
                  revision: revisionRef.current,
                });
            }}
            type="button"
          >
            Retry autosave
          </button>
        </div>
      ) : null}
      <fieldset
        className="curator-split"
        data-active-pane={activePane}
        disabled={conflictRecoveryPending}
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
                    (save.isPending || publish.isPending)
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
                    selectedIDRef.current = event.id;
                    save.reset();
                    latestDraftRef.current = undefined;
                    localDuringConflict.current = undefined;
                    setDraft(undefined);
                    setSelectedMedia(new Set());
                    setDestination("unassigned");
                    setNewMomentDay("");
                    setInspectedMomentID("");
                    setRevision(0);
                    setSaveState("saved");
                    setConflictRecoveryError("");
                    setConflictRecoveryPending(false);
                    setMergeError("");
                    setNotifyRecipients(true);
                    setPreviewRecipientID("");
                    setPreviewOpen(false);
                    publish.reset();
                    withdraw.reset();
                    setWithdrawTarget(undefined);
                    setWithdrawReason("");
                    setSelectedID(event.id);
                    setActivePane("organize");
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
              <button onClick={() => void eventQuery.refetch()} type="button">
                Retry loading Event
              </button>
            </div>
          ) : null}
          {currentDraft ? (
            <>
              <header>
                <div>
                  <p className="step-label">Active Event</p>
                  <h3>{currentDraft.title}</h3>
                </div>
                <Checklist event={currentDraft} />
              </header>
              <PlaceLabelEditor
                ariaLabel="Event Place labels"
                key={`event-place-labels-${currentDraft.id}`}
                labels={currentDraft.place_labels}
                onCommit={(labels) =>
                  change((next) => {
                    next.place_labels = labels;
                  })
                }
                placeholder="Paris, Jardin du Luxembourg"
              />
              <StagedUpdateReview event={currentDraft} />
              <div className="move-toolbar">
                <div className="move-control">
                  <label>
                    Move selected to
                    <select
                      onChange={(event) => setDestination(event.target.value)}
                      value={destination}
                    >
                      <option value="unassigned">Unassigned Media</option>
                      {currentDraft.moments.map((moment, index) => (
                        <option key={moment.id} value={moment.id}>
                          {moment.title || `Moment ${index + 1}`}
                        </option>
                      ))}
                    </select>
                  </label>
                  <button
                    disabled={selectedMedia.size === 0}
                    onClick={() => moveSelected()}
                    type="button"
                  >
                    Move selected Media
                  </button>
                </div>
                <div className="move-control">
                  <label>
                    New Moment day
                    <input
                      onChange={(event) => setNewMomentDay(event.target.value)}
                      type="date"
                      value={newMomentDay}
                    />
                  </label>
                  <button
                    disabled={selectedMedia.size === 0 || !newMomentDay}
                    onClick={createMomentFromSelected}
                    type="button"
                  >
                    Create Moment from selected Media
                  </button>
                </div>
              </div>
              {mergeError ? (
                <p className="form-error" role="alert">
                  {mergeError}
                </p>
              ) : null}
              <section className="moment-card unassigned">
                <h4>Unassigned Media</h4>
                <ul>
                  {currentDraft.unassigned_media.map((item) => (
                    <MediaRow
                      item={item}
                      key={item.id}
                      onMove={(direction) => reorderMedia(item.id, direction)}
                      onSelect={() =>
                        setSelectedMedia((current) => {
                          const next = new Set(current);
                          if (next.has(item.id)) next.delete(item.id);
                          else next.add(item.id);
                          return next;
                        })
                      }
                      selected={selectedMedia.has(item.id)}
                      stagedKind={stagedMediaKinds.get(item.id)}
                    />
                  ))}
                </ul>
              </section>
              <div className="moment-list">
                {currentDraft.moments.map((moment, index) => (
                  <article
                    className={`moment-card ${(stagedMomentKinds.get(moment.id) ?? []).map((kind) => `staged-${kind}`).join(" ")}`}
                    key={moment.id}
                  >
                    <header>
                      <div>
                        <p>
                          Moment {index + 1} · {moment.proposed_day}
                        </p>
                        <input
                          aria-label={`Title for Moment ${index + 1}`}
                          onChange={(event) =>
                            change((next) => {
                              next.moments[index].title = event.target.value;
                            })
                          }
                          placeholder={`Moment ${index + 1}`}
                          value={moment.title}
                        />
                        <PlaceLabelEditor
                          ariaLabel={`Place labels for Moment ${index + 1}`}
                          key={`moment-place-labels-${moment.id}`}
                          labels={moment.place_labels}
                          onCommit={(labels) =>
                            change((next) => {
                              const target = next.moments.find(
                                (candidate) => candidate.id === moment.id,
                              );
                              if (target) target.place_labels = labels;
                            })
                          }
                          placeholder="Place labels, comma-separated"
                        />
                      </div>
                      <div className="row-actions">
                        <button
                          aria-label={`Move Moment ${index + 1} earlier`}
                          onClick={() => reorderMoment(index, -1)}
                          type="button"
                        >
                          ↑
                        </button>
                        <button
                          aria-label={`Move Moment ${index + 1} later`}
                          onClick={() => reorderMoment(index, 1)}
                          type="button"
                        >
                          ↓
                        </button>
                      </div>
                    </header>
                    <label>
                      Cover
                      <select
                        onChange={(event) =>
                          change((next) => {
                            next.moments[index].cover_media_item_id =
                              event.target.value || null;
                          })
                        }
                        value={moment.cover_media_item_id ?? ""}
                      >
                        <option value="">Choose a cover</option>
                        {moment.media_items.map((item) => (
                          <option key={item.id} value={item.id}>
                            {mediaLabel(item)}
                          </option>
                        ))}
                      </select>
                    </label>
                    <ul>
                      {moment.media_items.map((item) => (
                        <MediaRow
                          item={item}
                          key={item.id}
                          onMove={(direction) =>
                            reorderMedia(item.id, direction)
                          }
                          onSelect={() =>
                            setSelectedMedia((current) => {
                              const next = new Set(current);
                              if (next.has(item.id)) next.delete(item.id);
                              else next.add(item.id);
                              return next;
                            })
                          }
                          selected={selectedMedia.has(item.id)}
                          stagedKind={stagedMediaKinds.get(item.id)}
                        />
                      ))}
                    </ul>
                    <div className="moment-actions">
                      <button
                        disabled={
                          moment.media_items.filter((item) =>
                            selectedMedia.has(item.id),
                          ).length === 0 ||
                          moment.media_items.every((item) =>
                            selectedMedia.has(item.id),
                          )
                        }
                        onClick={() => splitMoment(moment)}
                        type="button"
                      >
                        Split selected into new Moment
                      </button>
                      <button
                        disabled={index === 0}
                        onClick={() => mergeWithPrevious(index)}
                        type="button"
                      >
                        Merge with previous Moment
                      </button>
                      <button
                        disabled={saveState !== "saved"}
                        onClick={() => {
                          setInspectedMomentID(moment.id);
                          setActivePane("inspect");
                        }}
                        type="button"
                      >
                        Inspect Attendance and Audience
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            </>
          ) : null}
        </section>
        <aside
          className="inspect-pane"
          id="inspect-pane"
          ref={inspectPaneRef}
          tabIndex={-1}
        >
          <h3>Attendance and Audience</h3>
          {!inspected ? (
            <p>Choose a Moment to inspect.</p>
          ) : (
            <>
              <p>{inspected.title || inspected.proposed_day}</p>
              <AttendanceAudienceReview
                key={inspected.id}
                csrfToken={session.csrf_token}
                momentID={inspected.id}
                onAttendanceConfirmed={() =>
                  reflectReview((next) => {
                    const moment = next.moments.find(
                      (candidate) => candidate.id === inspected.id,
                    );
                    if (moment) {
                      moment.attendance_complete = true;
                      moment.audience_complete = false;
                    }
                  })
                }
                onAudienceChanged={() =>
                  reflectReview((next) => {
                    const moment = next.moments.find(
                      (candidate) => candidate.id === inspected.id,
                    );
                    if (moment) moment.audience_complete = false;
                  })
                }
                onAudienceApproved={() =>
                  reflectReview((next) => {
                    const moment = next.moments.find(
                      (candidate) => candidate.id === inspected.id,
                    );
                    if (moment) moment.audience_complete = true;
                  })
                }
              />
              <p>{inspected.media_items.length} Media items in this Moment.</p>
            </>
          )}
          {currentDraft ? (
            <>
              <label className="inspection-check final-review">
                <input
                  checked={currentDraft.final_review_complete}
                  onChange={(event) =>
                    change((next) => {
                      next.final_review_complete = event.target.checked;
                    })
                  }
                  type="checkbox"
                />
                Final review complete
              </label>
              {currentDraft.lifecycle === "published" ? (
                <section
                  aria-labelledby="withdrawal-actions-title"
                  className="publication-actions withdrawal-actions"
                >
                  <h4 id="withdrawal-actions-title">Withdraw access</h4>
                  <p>
                    Withdrawal takes effect immediately. Restoration is not a
                    toggle and requires newly reviewed Audiences plus a fresh
                    Publication for every Event where the identity is currently
                    placed. Reused Media may require several Publications.
                  </p>
                  <label>
                    Currently published target
                    <select
                      disabled={withdrawalTargets.length === 0}
                      onChange={(event) => {
                        setWithdrawTarget(
                          withdrawalTargets.find(
                            (target) => target.target_id === event.target.value,
                          ),
                        );
                        withdraw.reset();
                      }}
                      value={selectedWithdrawTarget?.target_id ?? ""}
                    >
                      {withdrawalTargets.length === 0 ? (
                        <option value="">No targets available</option>
                      ) : null}
                      {withdrawalTargets.map((target) => (
                        <option
                          key={`${target.target_kind}:${target.target_id}`}
                          value={target.target_id}
                        >
                          {target.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    Attributable reason
                    <textarea
                      maxLength={1000}
                      onChange={(event) =>
                        setWithdrawReason(event.target.value)
                      }
                      required
                      value={withdrawReason}
                    />
                  </label>
                  <button
                    disabled={
                      saveState !== "saved" ||
                      withdraw.isPending ||
                      !selectedWithdrawTarget ||
                      !withdrawReason.trim()
                    }
                    onClick={() => {
                      if (
                        selectedWithdrawTarget &&
                        window.confirm(
                          "Withdraw Recipient access immediately? Identity and history will be preserved.",
                        )
                      ) {
                        withdraw.mutate(selectedWithdrawTarget);
                      }
                    }}
                    type="button"
                  >
                    {withdraw.isPending ? "Withdrawing…" : "Withdraw access"}
                  </button>
                  {withdraw.isError ? (
                    <p className="form-error" role="alert">
                      {withdraw.error.message}
                    </p>
                  ) : null}
                  {withdraw.data ? (
                    <p role="status">
                      Access withdrawn for{" "}
                      {withdraw.data.affected_recipient_count} Recipients across{" "}
                      {withdraw.data.affected_media_count} Media items.
                      Withdrawal created no new external notification. A
                      delivery already handed off before it committed may still
                      arrive.
                    </p>
                  ) : null}
                  {currentDraft.withdrawals.length > 0 ? (
                    <div>
                      <h5>Withdrawal history</h5>
                      <ul>
                        {currentDraft.withdrawals.map((item) => (
                          <li key={item.id}>
                            <strong>{item.target_kind}</strong>: {item.reason}{" "}
                            by {item.withdrawn_by_name}.{" "}
                            {item.restored_at
                              ? "Restored by a later Publication."
                              : "Access remains withdrawn."}
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                </section>
              ) : null}
              <section
                aria-labelledby="publication-actions-title"
                className="publication-actions"
              >
                <h4 id="publication-actions-title">Publication</h4>
                {currentDraft.published_attendance_recovery_required ? (
                  <p className="form-error" role="alert">
                    Person search is unavailable for this existing Publication
                    because its Attendance cannot be reconstructed safely.
                    Review and publish the Event again to restore it.
                  </p>
                ) : null}
                <label>
                  <input
                    checked={notifyRecipients}
                    onChange={(event) =>
                      setNotifyRecipients(event.target.checked)
                    }
                    type="checkbox"
                  />
                  Include notification intent
                </label>
                <button
                  disabled={
                    saveState !== "saved" ||
                    publish.isPending ||
                    (currentDraft.lifecycle === "published" &&
                      currentDraft.staged_update === null) ||
                    (publish.data?.editable_version ??
                      currentDraft.published_editable_version) ===
                      currentDraft.version ||
                    currentDraft.unassigned_media.length > 0 ||
                    currentDraft.moments.length === 0 ||
                    currentDraft.moments.some(
                      (moment) => !moment.audience_complete,
                    ) ||
                    !currentDraft.final_review_complete
                  }
                  onClick={() => publish.mutate(currentDraft)}
                  type="button"
                >
                  {publish.isPending ? "Publishing…" : "Publish Event"}
                </button>
                {publish.isError ? (
                  <p className="form-error" role="alert">
                    {publish.error.message}
                  </p>
                ) : null}
                {publish.data ? (
                  <p role="status">
                    Published revision {publish.data.revision} atomically.
                  </p>
                ) : null}
                <label>
                  Preview Recipient
                  <select
                    disabled={previewRecipients.isPending}
                    onChange={(event) => {
                      setPreviewRecipientID(event.target.value);
                      setPreviewOpen(false);
                    }}
                    value={previewRecipientID}
                  >
                    <option value="">Choose a Recipient</option>
                    {previewRecipients.data?.recipients.map((recipient) => (
                      <option
                        key={recipient.access_id}
                        value={recipient.person_id}
                      >
                        {recipient.name} ({recipient.access_state})
                      </option>
                    ))}
                  </select>
                </label>
                {previewRecipients.isPending ? (
                  <p>Loading preview Recipients…</p>
                ) : null}
                {previewRecipients.isError ? (
                  <div className="form-error" role="alert">
                    <p>{previewRecipients.error.message}</p>
                    <button
                      onClick={() => void previewRecipients.refetch()}
                      type="button"
                    >
                      Retry Recipient list
                    </button>
                  </div>
                ) : null}
                {previewRecipients.data?.recipients.length === 0 ? (
                  <p>No current Recipients available for preview.</p>
                ) : null}
                {previewRecipients.data?.recipients.some(
                  (recipient) =>
                    recipient.person_id === previewRecipientID &&
                    recipient.access_state !== "completed",
                ) ? (
                  <p>
                    Pending Recipient: cannot access yet. Preview shows approved
                    content after Onboarding.
                  </p>
                ) : null}
                <button
                  disabled={
                    !previewRecipientID ||
                    saveState !== "saved" ||
                    currentDraft.moments.length === 0 ||
                    currentDraft.moments.some(
                      (moment) => !moment.audience_complete,
                    )
                  }
                  onClick={() => setPreviewOpen(true)}
                  type="button"
                >
                  Preview as Recipient
                </button>
                {previewOpen ? (
                  <section
                    aria-label="Read-only Recipient preview"
                    className="recipient-preview"
                  >
                    <header>
                      <strong>Preview as Recipient</strong>
                      <span>Read only</span>
                    </header>
                    {preview.isPending ? <p>Loading preview…</p> : null}
                    {preview.isError ? (
                      <p className="form-error" role="alert">
                        {preview.error.message}
                      </p>
                    ) : null}
                    {preview.data && !preview.data.authorized ? (
                      <p>Nothing is shared with this Recipient.</p>
                    ) : null}
                    {preview.data?.authorized ? (
                      <>
                        <h5>{preview.data.title}</h5>
                        <p>{preview.data.media_count} authorized Media items</p>
                        <ol>
                          {preview.data.media.map((item) => (
                            <li key={item.id}>
                              {item.media_type}
                              {item.available ? "" : " (unavailable)"}
                            </li>
                          ))}
                        </ol>
                      </>
                    ) : null}
                    <div aria-label="Disabled Recipient interactions">
                      <button disabled type="button">
                        Comment
                      </button>
                      <button disabled type="button">
                        Favorite
                      </button>
                      <button disabled type="button">
                        Settings
                      </button>
                      <button disabled type="button">
                        Download
                      </button>
                    </div>
                    <p>
                      Preview activity is not recorded as Recipient engagement.
                    </p>
                  </section>
                ) : null}
              </section>
            </>
          ) : null}
          <p className="visually-hidden">{allMedia.length} total Media items</p>
        </aside>
      </fieldset>
    </section>
  );
}
