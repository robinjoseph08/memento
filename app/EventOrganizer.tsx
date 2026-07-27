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
} from "./types/generated/events";
import type { SessionResponse } from "./types/generated/setup";

type Pane = "work" | "organize" | "inspect";
type SaveState = "saved" | "saving" | "unsaved" | "failed" | "conflict";
type SaveAttempt = { event: DraftEvent; revision: number };

function mediaLabel(item: MediaItem) {
  if (!item.local_date_time) return `Undated ${item.media_type}`;
  const parsed = new Date(item.local_date_time);
  return Number.isNaN(parsed.valueOf())
    ? `Undated ${item.media_type}`
    : `${item.media_type}, ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed)}`;
}

function cloneEvent(event: DraftEvent): DraftEvent {
  return structuredClone(event);
}

function organizationRequest(event: DraftEvent): OrganizeEventRequest {
  return {
    version: event.version,
    moments: event.moments.map((moment) => ({
      id: moment.id,
      title: moment.title,
      proposed_day: moment.proposed_day,
      cover_media_item_id: moment.cover_media_item_id,
      media_item_ids: moment.media_items.map((item) => item.id),
    })),
    unassigned_media_ids: event.unassigned_media.map((item) => item.id),
    final_review_complete: event.final_review_complete,
  };
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
}: {
  item: MediaItem;
  selected: boolean;
  onSelect: () => void;
  onMove: (direction: -1 | 1) => void;
}) {
  return (
    <li
      className="media-row"
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
  const [revision, setRevision] = useState(0);
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
    const next = cloneEvent(currentDraft);
    mutator(next);
    latestDraftRef.current = next;
    queryClient.setQueryData(["event", next.id], next);
    setDraft(next);
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
    const previousID = currentDraft.moments[index - 1].id;
    const removedID = currentDraft.moments[index].id;
    change((next) => {
      const previous = next.moments[index - 1];
      const removed = next.moments[index];
      previous.media_items.push(...removed.media_items);
      next.moments.splice(index, 1);
    });
    if (destination === removedID) setDestination(previousID);
    setInspectedMomentID(previousID);
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
          <h3>Draft work</h3>
          {work.isPending ? <p>Loading drafts…</p> : null}
          {work.isError ? (
            <p className="form-error" role="alert">
              {work.error.message}
            </p>
          ) : null}
          {work.data?.events.length === 0 ? <p>No Event drafts yet.</p> : null}
          <ul className="event-list">
            {work.data?.events.map((event) => (
              <li key={event.id}>
                <button
                  aria-current={selectedID === event.id ? "page" : undefined}
                  disabled={event.id !== selectedID && save.isPending}
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
                    setSelectedID(event.id);
                    setActivePane("organize");
                  }}
                  type="button"
                >
                  <strong>{event.title}</strong>
                  <span>
                    {event.moment_count} Moments · {event.unassigned_count}{" "}
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
            <p className="pane-empty">Choose an Event draft from Work.</p>
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
                    />
                  ))}
                </ul>
              </section>
              <div className="moment-list">
                {currentDraft.moments.map((moment, index) => (
                  <article className="moment-card" key={moment.id}>
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
          ) : null}
          <p className="visually-hidden">{allMedia.length} total Media items</p>
        </aside>
      </fieldset>
    </section>
  );
}
