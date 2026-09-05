import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { Icon } from "../viewer-prototype/artwork";
import {
  AccessEditor,
  ChangeReview,
  StructureEditor,
  StudyDialog,
} from "./editors";
import {
  access,
  dateRange,
  orderedMoments,
  pendingRecommendations,
  people,
  type Decision,
  type Entry,
  type Moment,
  type Study,
} from "./fixtures";

type Save = (study: Study, message: string) => void;

function AccessInspector({
  study,
  moment,
  entry,
  onSave,
  onDirty,
  onCancelDirty,
  onMoment,
  onUndo,
  canUndo,
  revision,
}: {
  study: Study;
  moment: Moment;
  entry?: Entry;
  onSave: Save;
  onDirty: () => void;
  onCancelDirty: (action: () => void) => void;
  onMoment: () => void;
  onUndo: () => void;
  canUndo: boolean;
  revision: number;
}) {
  const [advanced, setAdvanced] = useState(false);
  const [showOthers, setShowOthers] = useState(false);
  const entries = entry
    ? [entry]
    : study.entries.filter((item) => item.moment === moment.id);
  const suggested = entry ? [] : pendingRecommendations(study, moment);
  const allowedAtScope = (person: string) => {
    const decision = entry?.decisions[person];
    if (decision && decision !== "inherit") return decision === "allow";
    const inherited = moment.decisions[person];
    return inherited && inherited !== "inherit"
      ? inherited === "allow"
      : study.albumAccess[person] === "allow";
  };
  const included = people.filter(allowedAtScope);
  const other = people.filter(
    (person) => !included.includes(person) && !suggested.includes(person),
  );
  const changeAccess = (names: string[], decision: Decision) => {
    const decisions = { ...(entry?.decisions ?? moment.decisions) };
    for (const person of names) decisions[person] = decision;
    const next = entry
      ? {
          ...study,
          entries: study.entries.map((item) =>
            item.id === entry.id ? { ...item, decisions } : item,
          ),
        }
      : {
          ...study,
          moments: study.moments.map((item) =>
            item.id === moment.id ? { ...item, decisions } : item,
          ),
        };
    onSave(
      next,
      decision === "allow"
        ? `Access added for ${names.join(", ")}.`
        : `Access denied for ${names.join(", ")}. Narrower overrides kept.`,
    );
    if (decision === "deny") setShowOthers(true);
    requestAnimationFrame(() =>
      document.getElementById(`quick-access-${names[0]}`)?.focus(),
    );
  };
  if (advanced)
    return (
      <AccessEditor
        entry={entry}
        key={revision}
        moment={moment}
        onCancel={() => onCancelDirty(() => setAdvanced(false))}
        onDirty={onDirty}
        onSave={(next, message) => {
          onSave(next, message);
          setAdvanced(false);
        }}
        study={study}
      />
    );
  const personRow = (person: string) => {
    const count = entries.filter(
      (item) => access(study, item, person).allowed,
    ).length;
    const decision = (entry?.decisions ?? moment.decisions)[person];
    const checked = allowedAtScope(person);
    const inherited = (!decision || decision === "inherit") && checked;
    return (
      <label className="quick-person" key={person}>
        <input
          aria-label={`Allow ${person} for ${entry ? "this item" : "this Moment"}`}
          checked={checked}
          id={`quick-access-${person}`}
          onChange={(event) =>
            changeAccess([person], event.target.checked ? "allow" : "deny")
          }
          type="checkbox"
        />
        <span className="viewer-avatar">{person[0]}</span>
        <span>
          <strong>{person}</strong>
          <small>
            {suggested.includes(person)
              ? "Detected here, not shared yet"
              : `${count} of ${entries.length} items${inherited ? (entry ? ", inherited" : ", Album access") : decision === "deny" ? ", denied here" : ""}`}
          </small>
        </span>
      </label>
    );
  };
  return (
    <>
      {entry ? (
        <>
          <button className="text-button" onClick={onMoment}>
            <Icon name="back" />
            Moment access
          </button>
          <img
            alt={entry.label}
            className="inspector-image"
            src={entry.image}
          />
          <h2>{entry.label}</h2>
        </>
      ) : (
        <>
          <p className="curator-hint">Moment access</p>
          <h2>{moment.title}</h2>
        </>
      )}
      <div className="inspector-intro">
        <p className="curator-hint">Changes save immediately.</p>
        <button className="text-button" disabled={!canUndo} onClick={onUndo}>
          Undo last change
        </button>
      </div>
      <form
        aria-label={entry ? "Quick item access" : "Quick Moment access"}
        className="quick-access"
        onSubmit={(event) => event.preventDefault()}
      >
        <fieldset>
          <legend>
            Allowed <span>{included.length}</span>
          </legend>
          {included.length ? (
            included.map(personRow)
          ) : (
            <p className="curator-hint">No access at this scope yet.</p>
          )}
        </fieldset>
        {suggested.length > 0 && (
          <fieldset className="suggestion-group">
            <legend>
              Suggested <span>{suggested.length}</span>
            </legend>
            <button
              className="text-button add-suggested"
              onClick={() => changeAccess(suggested, "allow")}
              type="button"
            >
              Add all suggested
            </button>
            {suggested.map(personRow)}
          </fieldset>
        )}
        {other.length > 0 && (
          <>
            <button
              aria-expanded={showOthers}
              className="text-button"
              onClick={() => setShowOthers(!showOthers)}
              type="button"
            >
              {showOthers ? "Hide other people" : "Add someone else"}
            </button>
            {showOthers && (
              <fieldset>
                <legend>Other people</legend>
                {other.map(personRow)}
              </fieldset>
            )}
          </>
        )}
      </form>
      <details className="access-explanation">
        <summary>How access works</summary>
        <p>
          Checking a person explicitly allows this {entry ? "item" : "Moment"}.
          Unchecking explicitly denies it, even if Album access allows it.
          {!entry && " Item overrides still win."}
        </p>
        {!study.published && (
          <p>
            This album is unpublished. These decisions apply after publication.
          </p>
        )}
      </details>
      <button className="text-button" onClick={() => setAdvanced(true)}>
        Rules & exceptions
      </button>
    </>
  );
}

export function Workbench({
  study,
  moment,
  revision,
  onSave,
  onDirty,
  onCancelDirty,
  onUndo,
  canUndo,
}: {
  study: Study;
  moment: Moment;
  revision: number;
  onSave: Save;
  onDirty: () => void;
  onCancelDirty: (action: () => void) => void;
  onUndo: () => void;
  canUndo: boolean;
}) {
  const [params, setParams] = useSearchParams();
  const [selected, setSelected] = useState<string[]>([]);
  const [operation, setOperation] = useState<"move" | "split" | "merge" | null>(
    null,
  );
  const [rename, setRename] = useState(false);
  const [coverReview, setCoverReview] = useState(false);
  const entries = study.entries.filter((item) => item.moment === moment.id);
  const entry = entries.find((item) => item.id === params.get("entry"));
  const sheet = params.get("inspect") === "1";
  const collapsed = params.get("collapsed") === "1";
  const all = params.get("media") === "all";
  const selectedIds = selected.filter((id) =>
    entries.some((item) => item.id === id),
  );
  const selectedCover =
    selectedIds.length === 1
      ? entries.find((item) => item.id === selectedIds[0])
      : undefined;
  const coverAfter = {
    ...study,
    moments: study.moments.map((item) =>
      item.id === moment.id && selectedCover
        ? { ...item, cover: selectedCover.id }
        : item,
    ),
  };
  const update = (changes: Record<string, string | null>) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      for (const [key, value] of Object.entries(changes)) {
        if (value === null) next.delete(key);
        else next.set(key, value);
      }
      return next;
    });
  const inspector = (
    <AccessInspector
      canUndo={canUndo}
      entry={entry}
      key={`${moment.id}-${entry?.id ?? "moment"}`}
      moment={moment}
      onCancelDirty={onCancelDirty}
      onDirty={onDirty}
      onMoment={() => update({ entry: null })}
      onSave={onSave}
      onUndo={onUndo}
      revision={revision}
      study={study}
    />
  );
  const startOperation = (next: "move" | "split" | "merge") =>
    onCancelDirty(() => {
      onDirty();
      setOperation(next);
    });
  return (
    <div className="curator-workbench">
      <section aria-label="Moment review" className="moment-canvas">
        <header className="canvas-heading">
          <div>
            <h2>Moments</h2>
            <p className="curator-hint">
              Review access by Moment. Select media only when you need an
              exception or a move.
            </p>
          </div>
          <span className="curator-hint">{study.moments.length} Moments</span>
        </header>
        <div className="moment-cards">
          {orderedMoments(study).map((item) => {
            const media = study.entries.filter(
              (entry) => entry.moment === item.id,
            );
            const isCurrent = item.id === moment.id;
            const expanded = isCurrent && !collapsed;
            const currentCover = media.find((entry) => entry.id === item.cover);
            const suggestions = pendingRecommendations(study, item);
            const audience = people.filter((person) =>
              media.some((entry) => access(study, entry, person).allowed),
            );
            const shown = expanded ? (all ? media : media.slice(0, 24)) : [];
            const next = new URLSearchParams(params);
            next.set("moment", item.id);
            next.delete("entry");
            next.delete("inspect");
            next.delete("media");
            if (expanded) next.set("collapsed", "1");
            else next.delete("collapsed");
            return (
              <article
                aria-label={item.title}
                className={`moment-card ${isCurrent ? "is-current" : ""}`}
                key={item.id}
              >
                <header className="moment-card-header">
                  <Link
                    aria-expanded={expanded}
                    className="moment-card-title"
                    to={`?${next}`}
                  >
                    {currentCover && <img alt="" src={currentCover.image} />}
                    <span>
                      <strong>{item.title}</strong>
                      <small>
                        {dateRange(media)} /{" "}
                        {media.filter((entry) => entry.kind === "photo").length}{" "}
                        photos
                        {media.some((entry) => entry.kind === "video") &&
                          `, ${media.filter((entry) => entry.kind === "video").length} videos`}
                      </small>
                    </span>
                    <Icon name={expanded ? "back" : "next"} />
                  </Link>
                  <button
                    aria-label={`Access for ${item.title}`}
                    className="moment-access-trigger"
                    onClick={() =>
                      update({
                        moment: item.id,
                        entry: null,
                        inspect: window.innerWidth < 1000 ? "1" : null,
                        collapsed: null,
                        media: null,
                      })
                    }
                  >
                    <span>{audience.join(", ") || "No access yet"}</span>
                    <small>
                      {suggestions.length
                        ? `${suggestions.length} suggested`
                        : "Access"}
                    </small>
                  </button>
                </header>
                {expanded && (
                  <>
                    <form
                      aria-label={`Select media in ${item.title}`}
                      className="media-selection"
                      onSubmit={(event) => event.preventDefault()}
                    >
                      <div className="selection-toolbar">
                        <label className="curator-check">
                          <input
                            aria-label={`Select all ${media.length} items`}
                            checked={selectedIds.length === media.length}
                            onChange={(event) =>
                              setSelected(
                                event.target.checked
                                  ? media.map((item) => item.id)
                                  : [],
                              )
                            }
                            type="checkbox"
                          />
                          {selectedIds.length
                            ? `${selectedIds.length} selected`
                            : "Select all"}
                        </label>
                        {selectedIds.length > 0 ? (
                          <div className="curator-buttons">
                            <button
                              className="curator-button"
                              disabled={study.moments.length < 2}
                              onClick={() => startOperation("move")}
                              type="button"
                            >
                              Move
                            </button>
                            <button
                              className="curator-button"
                              disabled={selectedIds.length === entries.length}
                              onClick={() => startOperation("split")}
                              type="button"
                            >
                              Split
                            </button>
                            {selectedCover && (
                              <button
                                className="curator-button"
                                disabled={selectedCover.id === moment.cover}
                                onClick={() =>
                                  onCancelDirty(() => setCoverReview(true))
                                }
                                type="button"
                              >
                                Set as cover
                              </button>
                            )}
                            <button
                              className="text-button"
                              onClick={() => setSelected([])}
                              type="button"
                            >
                              Clear
                            </button>
                          </div>
                        ) : (
                          <div className="curator-buttons">
                            <button
                              aria-label="Rename Moment"
                              className="text-button"
                              onClick={() =>
                                onCancelDirty(() => setRename(true))
                              }
                              type="button"
                            >
                              Rename
                            </button>
                            <button
                              className="text-button"
                              disabled={study.moments.length < 2}
                              onClick={() => startOperation("merge")}
                              type="button"
                            >
                              Merge
                            </button>
                          </div>
                        )}
                      </div>
                      <div className="curator-media-grid">
                        {shown.map((media) => (
                          <div
                            className={`curator-media-item ${selectedIds.includes(media.id) ? "is-selected" : ""}`}
                            key={media.id}
                          >
                            <label className="media-select-target">
                              <input
                                aria-label={`Select ${media.label}`}
                                checked={selectedIds.includes(media.id)}
                                onChange={(event) =>
                                  setSelected(
                                    event.target.checked
                                      ? [...selectedIds, media.id]
                                      : selectedIds.filter(
                                          (id) => id !== media.id,
                                        ),
                                  )
                                }
                                type="checkbox"
                              />
                              <img
                                alt={media.label}
                                loading="lazy"
                                src={media.image}
                              />
                              {media.id === moment.cover && (
                                <span className="media-badge">Cover</span>
                              )}
                              {media.kind === "video" && (
                                <span className="media-kind">
                                  <Icon name="video" />
                                </span>
                              )}
                            </label>
                            <button
                              aria-label={`Inspect ${media.label}`}
                              className="media-inspect"
                              onClick={() =>
                                update({
                                  entry: media.id,
                                  ...(window.innerWidth < 1000
                                    ? { inspect: "1" }
                                    : {}),
                                })
                              }
                              type="button"
                            >
                              <span>{media.id.toUpperCase()}</span>
                              <span>
                                {Object.values(media.decisions).some(
                                  (decision) => decision !== "inherit",
                                )
                                  ? "Override"
                                  : "Details"}
                              </span>
                            </button>
                          </div>
                        ))}
                      </div>
                    </form>
                    <footer className="moment-card-footer">
                      <span>
                        {shown.length} of {media.length} items shown
                        {selectedIds.some(
                          (id) => !shown.some((item) => item.id === id),
                        ) &&
                          ` / ${selectedIds.filter((id) => !shown.some((item) => item.id === id)).length} selected outside this view`}
                      </span>
                      {media.length > 24 && (
                        <button
                          className="text-button"
                          onClick={() => update({ media: all ? null : "all" })}
                        >
                          {all
                            ? "Show fewer"
                            : `Show all ${media.length} items`}
                        </button>
                      )}
                    </footer>
                  </>
                )}
              </article>
            );
          })}
        </div>
      </section>
      <aside aria-label="Access inspector" className="access-inspector">
        {!sheet && inspector}
      </aside>
      {sheet && (
        <StudyDialog
          className="access-sheet"
          onClose={() => update({ inspect: null })}
          title={entry ? "Item access" : "Moment access"}
        >
          {inspector}
        </StudyDialog>
      )}
      {operation && (
        <StructureEditor
          key={operation}
          moment={moment}
          onClose={() => onCancelDirty(() => setOperation(null))}
          onDirty={onDirty}
          onSave={(next, message) => {
            onSave(next, message);
            setOperation(null);
            setSelected([]);
          }}
          operation={operation}
          selected={selectedIds}
          study={study}
        />
      )}
      {rename && (
        <StudyDialog
          onClose={() => onCancelDirty(() => setRename(false))}
          title="Rename Moment"
        >
          <form
            className="curator-form"
            onChange={onDirty}
            onSubmit={(event) => {
              event.preventDefault();
              const data = new FormData(event.currentTarget);
              const title =
                String(data.get("title") ?? "").trim() || dateRange(entries);
              onSave(
                {
                  ...study,
                  moments: study.moments.map((item) =>
                    item.id === moment.id ? { ...item, title } : item,
                  ),
                },
                "Moment renamed. Viewer date headings stay unchanged.",
              );
              setRename(false);
            }}
          >
            <label>
              Moment title
              <input defaultValue={moment.title} name="title" />
            </label>
            <p className="curator-hint">
              Only Curators see this name. Clear it to use the capture date.
            </p>
            <div className="curator-buttons">
              <button className="action-button" type="submit">
                Save Moment title
              </button>
              <button
                className="curator-button"
                onClick={() => onCancelDirty(() => setRename(false))}
                type="button"
              >
                Cancel
              </button>
            </div>
          </form>
        </StudyDialog>
      )}
      {coverReview && selectedCover && (
        <StudyDialog
          onClose={() => setCoverReview(false)}
          title="Change Moment cover?"
        >
          <p>
            Use {selectedCover.label} as the cover of {moment.title}.
          </p>
          <ChangeReview after={coverAfter} before={study} />
          <div className="curator-buttons">
            <button
              className="action-button"
              onClick={() => {
                onSave(coverAfter, "Moment cover changed.");
                setCoverReview(false);
              }}
            >
              Save Moment cover
            </button>
            <button
              className="curator-button"
              onClick={() => setCoverReview(false)}
            >
              Cancel
            </button>
          </div>
        </StudyDialog>
      )}
    </div>
  );
}
