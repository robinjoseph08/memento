import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";

import { APIError, apiJSON } from "./api";
import type {
  Event as DraftEvent,
  EventListResponse,
  MediaItem,
  Moment,
  OrganizeEventRequest,
} from "./types/generated/events";
import type { SessionResponse } from "./types/generated/setup";

type Pane = "work" | "organize" | "inspect";
type SaveState = "saved" | "saving" | "unsaved" | "conflict";

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
      attendance_complete: moment.attendance_complete,
      audience_complete: moment.audience_complete,
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

export function EventOrganizer({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [selectedID, setSelectedID] = useState("");
  const [draft, setDraft] = useState<DraftEvent>();
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [destination, setDestination] = useState("unassigned");
  const [inspectedMomentID, setInspectedMomentID] = useState("");
  const [activePane, setActivePane] = useState<Pane>("work");
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [revision, setRevision] = useState(0);
  const localDuringConflict = useRef<DraftEvent | undefined>(undefined);

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

  const currentDraft = draft ?? eventQuery.data;

  const save = useMutation({
    mutationFn: (event: DraftEvent) =>
      apiJSON<DraftEvent>(`/api/events/${event.id}/organization`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(organizationRequest(event)),
      }),
    onMutate: () => setSaveState("saving"),
    onSuccess: (saved) => {
      setDraft(cloneEvent(saved));
      setSaveState("saved");
      setRevision(0);
      queryClient.setQueryData(["event", saved.id], saved);
      void queryClient.invalidateQueries({ queryKey: ["events"] });
    },
    onError: (error, attempted) => {
      if (error instanceof APIError && error.status === 409) {
        localDuringConflict.current = attempted;
        setSaveState("conflict");
      } else {
        setSaveState("unsaved");
      }
    },
  });

  const saveDraft = save.mutate;
  useEffect(() => {
    if (!currentDraft || revision === 0 || saveState === "conflict") return;
    const timer = window.setTimeout(
      () => saveDraft(cloneEvent(currentDraft)),
      450,
    );
    return () => window.clearTimeout(timer);
  }, [currentDraft, revision, saveState, saveDraft]);

  function change(mutator: (next: DraftEvent) => void) {
    if (!currentDraft) return;
    const next = cloneEvent(currentDraft);
    mutator(next);
    setDraft(next);
    setSaveState("unsaved");
    setRevision((value) => value + 1);
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

  function moveSelected(targetID = destination) {
    if (selectedMedia.size === 0) return;
    change((next) => {
      const moving: MediaItem[] = [];
      for (const id of selectedMedia) {
        const located = locateMedia(next, id);
        if (located.index >= 0)
          moving.push(...located.items.splice(located.index, 1));
      }
      if (targetID === "unassigned") next.unassigned_media.push(...moving);
      else
        next.moments
          .find((moment) => moment.id === targetID)
          ?.media_items.push(...moving);
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
    change((next) => {
      const previous = next.moments[index - 1];
      const removed = next.moments[index];
      previous.media_items.push(...removed.media_items);
      next.moments.splice(index, 1);
    });
    setInspectedMomentID(currentDraft.moments[index - 1].id);
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
    localDuringConflict.current = undefined;
    const result = await eventQuery.refetch();
    if (result.data) {
      setDraft(cloneEvent(result.data));
      setRevision(0);
      setSaveState("saved");
    }
  }

  async function keepMyChanges() {
    const local = localDuringConflict.current;
    if (!local) return;
    const result = await eventQuery.refetch();
    if (!result.data) return;
    local.version = result.data.version;
    setDraft(cloneEvent(local));
    localDuringConflict.current = undefined;
    setSaveState("unsaved");
    setRevision((value) => value + 1);
  }

  const inspected =
    currentDraft?.moments.find((moment) => moment.id === inspectedMomentID) ??
    currentDraft?.moments[0];
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
                : "Changes not saved yet"}
        </p>
      </header>
      <nav aria-label="Mobile workspace panes" className="mobile-pane-nav">
        {(["work", "organize", "inspect"] as Pane[]).map((pane) => (
          <button
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
          <button onClick={() => void loadNewerVersion()} type="button">
            Load newer version
          </button>
          <button onClick={() => void keepMyChanges()} type="button">
            Keep my changes
          </button>
        </div>
      ) : save.isError ? (
        <div className="form-error" role="alert">
          <p>{save.error.message}</p>
          <button
            disabled={!currentDraft || save.isPending}
            onClick={() => {
              if (currentDraft) save.mutate(cloneEvent(currentDraft));
            }}
            type="button"
          >
            Retry autosave
          </button>
        </div>
      ) : null}
      <div className="curator-split" data-active-pane={activePane}>
        <aside className="work-pane">
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
                  onClick={() => {
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
          className="organize-pane"
          aria-label="Active Event organization"
        >
          {!selectedID ? (
            <p className="pane-empty">Choose an Event draft from Work.</p>
          ) : null}
          {eventQuery.isPending && selectedID ? <p>Loading Event…</p> : null}
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
                        disabled={selectedMedia.size === 0}
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
        <aside className="inspect-pane">
          <h3>Attendance and Audience</h3>
          {!inspected ? (
            <p>Choose a Moment to inspect.</p>
          ) : (
            <>
              <p>{inspected.title || inspected.proposed_day}</p>
              <label className="inspection-check">
                <input
                  checked={inspected.attendance_complete}
                  onChange={(event) =>
                    change((next) => {
                      const moment = next.moments.find(
                        (candidate) => candidate.id === inspected.id,
                      );
                      if (moment)
                        moment.attendance_complete = event.target.checked;
                    })
                  }
                  type="checkbox"
                />
                Attendance reviewed
              </label>
              <label className="inspection-check">
                <input
                  checked={inspected.audience_complete}
                  onChange={(event) =>
                    change((next) => {
                      const moment = next.moments.find(
                        (candidate) => candidate.id === inspected.id,
                      );
                      if (moment)
                        moment.audience_complete = event.target.checked;
                    })
                  }
                  type="checkbox"
                />
                Audience reviewed, including empty Audience
              </label>
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
      </div>
    </section>
  );
}
