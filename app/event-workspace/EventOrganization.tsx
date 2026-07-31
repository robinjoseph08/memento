import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { Readiness } from "./Readiness";
import { StagedChangeLabels } from "./StagedReview";
import type {
  Event,
  MediaItem,
  Moment,
  StagedChange,
} from "../types/generated/events";

const maxPlaceLabels = 20;
const maxPlaceLabelLength = 120;

function mediaLabel(item: Pick<MediaItem, "media_type" | "local_date_time">) {
  if (!item.local_date_time) return `Undated ${item.media_type}`;
  const parsed = new Date(item.local_date_time);
  return Number.isNaN(parsed.valueOf())
    ? `Undated ${item.media_type}`
    : `${item.media_type}, ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed)}`;
}

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
        onChange={(input) => {
          setRaw(input.target.value);
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

function MediaRow({
  item,
  selected,
  onSelect,
  onMove,
  stagedKinds,
}: {
  item: MediaItem;
  selected: boolean;
  onSelect: () => void;
  onMove: (direction: -1 | 1) => void;
  stagedKinds: StagedChange["kind"][];
}) {
  return (
    <li
      className={`media-row ${stagedKinds.map((kind) => `staged-${kind}`).join(" ")}`.trim()}
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
        <StagedChangeLabels kinds={stagedKinds} />
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

export function EventOrganization({
  event,
  hasUnsavedChanges,
  metadataValid,
  titleValidationError,
  timezoneValidationError,
  inspectionDisabled,
  stagedReview,
  onChange,
  onInspectionTargetChange,
}: {
  event: Event;
  hasUnsavedChanges: boolean;
  metadataValid: boolean;
  titleValidationError: string;
  timezoneValidationError: string;
  inspectionDisabled: boolean;
  stagedReview: ReactNode;
  onChange: (mutator: (next: Event) => void) => void;
  onInspectionTargetChange: (momentID: string, openPane: boolean) => void;
}) {
  const [selectedMedia, setSelectedMedia] = useState<Set<string>>(new Set());
  const [destination, setDestination] = useState("unassigned");
  const [newMomentDay, setNewMomentDay] = useState("");
  const [mergeError, setMergeError] = useState("");
  const stagedMediaKinds = useMemo(() => {
    const kinds = new Map<string, StagedChange["kind"][]>();
    for (const change of event.staged_update?.changes ?? []) {
      for (const mediaID of change.media_item_ids) {
        kinds.set(mediaID, [...(kinds.get(mediaID) ?? []), change.kind]);
      }
    }
    return kinds;
  }, [event.staged_update]);
  const stagedMomentKinds = useMemo(() => {
    const kinds = new Map<string, StagedChange["kind"][]>();
    for (const change of event.staged_update?.changes ?? []) {
      for (const momentID of change.moment_ids) {
        kinds.set(momentID, [...(kinds.get(momentID) ?? []), change.kind]);
      }
    }
    return kinds;
  }, [event.staged_update]);
  const allMedia = [
    ...event.moments.flatMap((moment) => moment.media_items),
    ...event.unassigned_media,
  ];

  function locateMedia(next: Event, id: string) {
    for (const moment of next.moments) {
      const index = moment.media_items.findIndex((item) => item.id === id);
      if (index >= 0) return { items: moment.media_items, index };
    }
    const index = next.unassigned_media.findIndex((item) => item.id === id);
    return { items: next.unassigned_media, index };
  }

  function reorderMedia(id: string, direction: -1 | 1) {
    onChange((next) => {
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

  function takeSelectedMedia(next: Event) {
    const moving: MediaItem[] = [];
    const takeFrom = (items: MediaItem[]) =>
      items.filter((item) => {
        if (!selectedMedia.has(item.id)) return true;
        moving.push(item);
        return false;
      });
    next.unassigned_media = takeFrom(next.unassigned_media);
    for (const moment of next.moments)
      moment.media_items = takeFrom(moment.media_items);
    return moving;
  }

  function repairMoments(next: Event) {
    next.moments = next.moments.filter(
      (moment) => moment.media_items.length > 0,
    );
    for (const moment of next.moments) {
      if (
        moment.cover_media_item_id &&
        !moment.media_items.some(
          (item) => item.id === moment.cover_media_item_id,
        )
      )
        moment.cover_media_item_id = null;
    }
  }

  function moveSelected(targetID = destination) {
    if (
      selectedMedia.size === 0 ||
      (targetID !== "unassigned" &&
        !event.moments.some((moment) => moment.id === targetID))
    )
      return;
    onChange((next) => {
      const moving = takeSelectedMedia(next);
      if (targetID === "unassigned") next.unassigned_media.push(...moving);
      else
        next.moments
          .find((moment) => moment.id === targetID)!
          .media_items.push(...moving);
      repairMoments(next);
    });
    setSelectedMedia(new Set());
  }

  function removeSelectedMedia() {
    if (selectedMedia.size === 0 || selectedMedia.size >= allMedia.length)
      return;
    onChange((next) => {
      takeSelectedMedia(next);
      repairMoments(next);
    });
    setSelectedMedia(new Set());
  }

  function createMomentFromSelected() {
    if (selectedMedia.size === 0 || !newMomentDay) return;
    const id = crypto.randomUUID();
    onChange((next) => {
      const moving = takeSelectedMedia(next);
      repairMoments(next);
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
    onInspectionTargetChange(id, false);
  }

  function splitMoment(moment: Moment) {
    const chosen = moment.media_items.filter((item) =>
      selectedMedia.has(item.id),
    );
    if (chosen.length === 0 || chosen.length === moment.media_items.length)
      return;
    const id = crypto.randomUUID();
    onChange((next) => {
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
    onInspectionTargetChange(id, false);
  }

  function mergeWithPrevious(index: number) {
    if (index < 1) return;
    const previousMoment = event.moments[index - 1];
    const removedMoment = event.moments[index];
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
    onChange((next) => {
      const previous = next.moments[index - 1];
      const removed = next.moments[index];
      previous.place_labels = placeLabels;
      previous.media_items.push(...removed.media_items);
      next.moments.splice(index, 1);
    });
    if (destination === removedMoment.id) setDestination(previousMoment.id);
    onInspectionTargetChange(previousMoment.id, false);
  }

  function reorderMoment(index: number, direction: -1 | 1) {
    onChange((next) => {
      const target = index + direction;
      if (target < 0 || target >= next.moments.length) return;
      [next.moments[index], next.moments[target]] = [
        next.moments[target],
        next.moments[index],
      ];
    });
  }

  const toggleMedia = (id: string) =>
    setSelectedMedia((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <>
      <header>
        <div>
          <p className="step-label">Active Event</p>
          <h3>{event.title}</h3>
        </div>
        <Readiness
          event={event}
          hasUnsavedChanges={hasUnsavedChanges}
          metadataValid={metadataValid}
        />
      </header>
      <PlaceLabelEditor
        ariaLabel="Event Place labels"
        key={`event-place-labels-${event.id}`}
        labels={event.place_labels}
        onCommit={(labels) =>
          onChange((next) => {
            next.place_labels = labels;
          })
        }
        placeholder="Paris, Jardin du Luxembourg"
      />
      {stagedReview}
      <section
        aria-labelledby="event-details-title"
        className="event-details-editor"
      >
        <h4 id="event-details-title">Event details</h4>
        <label>
          Event title
          <input
            aria-describedby={
              titleValidationError ? "event-title-error" : undefined
            }
            aria-invalid={titleValidationError !== ""}
            maxLength={240}
            onChange={(input) =>
              onChange((next) => {
                next.title = input.target.value;
              })
            }
            required
            type="text"
            value={event.title}
          />
          {titleValidationError ? (
            <small
              className="form-field-error"
              id="event-title-error"
              role="alert"
            >
              {titleValidationError}
            </small>
          ) : null}
        </label>
        <label>
          Event description
          <textarea
            maxLength={2000}
            onChange={(input) =>
              onChange((next) => {
                next.description = input.target.value;
              })
            }
            value={event.description}
          />
        </label>
        <label>
          Grouping timezone
          <input
            aria-describedby={
              timezoneValidationError ? "grouping-timezone-error" : undefined
            }
            aria-invalid={timezoneValidationError !== ""}
            maxLength={100}
            onChange={(input) =>
              onChange((next) => {
                next.grouping_timezone = input.target.value;
              })
            }
            required
            spellCheck={false}
            type="text"
            value={event.grouping_timezone}
          />
          {timezoneValidationError ? (
            <small
              className="form-field-error"
              id="grouping-timezone-error"
              role="alert"
            >
              {timezoneValidationError}
            </small>
          ) : null}
        </label>
      </section>
      <div className="move-toolbar">
        <div className="move-control">
          <label>
            Move selected to
            <select
              onChange={(input) => setDestination(input.target.value)}
              value={destination}
            >
              <option value="unassigned">Unassigned Media</option>
              {event.moments.map((moment, index) => (
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
          <button
            disabled={
              selectedMedia.size === 0 || selectedMedia.size >= allMedia.length
            }
            onClick={removeSelectedMedia}
            type="button"
          >
            Remove selected Media
          </button>
        </div>
        <div className="move-control">
          <label>
            New Moment day
            <input
              onChange={(input) => setNewMomentDay(input.target.value)}
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
          {event.unassigned_media.map((item) => (
            <MediaRow
              item={item}
              key={item.id}
              onMove={(direction) => reorderMedia(item.id, direction)}
              onSelect={() => toggleMedia(item.id)}
              selected={selectedMedia.has(item.id)}
              stagedKinds={stagedMediaKinds.get(item.id) ?? []}
            />
          ))}
        </ul>
      </section>
      <div className="moment-list">
        {event.moments.map((moment, index) => (
          <article
            className={`moment-card ${(stagedMomentKinds.get(moment.id) ?? []).map((kind) => `staged-${kind}`).join(" ")}`}
            key={moment.id}
          >
            <header>
              <div>
                <p>
                  Moment {index + 1} · {moment.proposed_day}
                </p>
                <StagedChangeLabels
                  kinds={stagedMomentKinds.get(moment.id) ?? []}
                />
                <input
                  aria-label={`Title for Moment ${index + 1}`}
                  onChange={(input) =>
                    onChange((next) => {
                      next.moments[index].title = input.target.value;
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
                    onChange((next) => {
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
              Cover (optional)
              <select
                aria-label="Cover"
                onChange={(input) =>
                  onChange((next) => {
                    next.moments[index].cover_media_item_id =
                      input.target.value || null;
                  })
                }
                value={moment.cover_media_item_id ?? ""}
              >
                <option value="">No cover selected</option>
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
                  onMove={(direction) => reorderMedia(item.id, direction)}
                  onSelect={() => toggleMedia(item.id)}
                  selected={selectedMedia.has(item.id)}
                  stagedKinds={stagedMediaKinds.get(item.id) ?? []}
                />
              ))}
            </ul>
            <div className="moment-actions">
              <button
                disabled={
                  moment.media_items.filter((item) =>
                    selectedMedia.has(item.id),
                  ).length === 0 ||
                  moment.media_items.every((item) => selectedMedia.has(item.id))
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
                disabled={inspectionDisabled}
                onClick={() => onInspectionTargetChange(moment.id, true)}
                type="button"
              >
                Inspect Attendance and Audience
              </button>
            </div>
          </article>
        ))}
      </div>
    </>
  );
}
