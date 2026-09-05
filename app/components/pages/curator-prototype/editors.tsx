import { useEffect, useRef, useState, type ReactNode } from "react";

import { Icon } from "../viewer-prototype/artwork";
import {
  access,
  accessDraft,
  audienceChanges,
  dateRange,
  detectedPeople,
  pendingRecommendations,
  people,
  viewerCover,
  visibleEntries,
  type Decision,
  type Decisions,
  type Entry,
  type Moment,
  type Study,
} from "./fixtures";

export function StudyDialog({
  title,
  onClose,
  children,
  className = "",
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  className?: string;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const opener = document.activeElement;
    const overflow = document.body.style.overflow;
    ref.current?.showModal();
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = overflow;
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    };
  }, []);
  return (
    <dialog
      aria-label={title}
      className={`curator-dialog ${className}`}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      ref={ref}
    >
      <header className="curator-row">
        <h2>{title}</h2>
        <button
          aria-label={`Close ${title}`}
          className="icon-button"
          onClick={onClose}
          type="button"
        >
          <Icon name="close" />
        </button>
      </header>
      {children}
    </dialog>
  );
}

export function ChangeReview({
  before,
  after,
}: {
  before: Study;
  after: Study;
}) {
  const changes = audienceChanges(before, after);
  const covers = people
    .map((person) => ({
      person,
      before: viewerCover(before, person),
      after: viewerCover(after, person),
    }))
    .filter((item) => item.before?.id !== item.after?.id);
  const changed = changes.some(
    (item) => item.gained.length || item.lost.length,
  );
  const names = (ids: string[]) =>
    ids
      .map((id) => before.entries.find((item) => item.id === id)?.label ?? id)
      .join(", ");
  return (
    <section aria-label="Visibility review" className="change-review">
      <h3>Visibility after saving</h3>
      {!changed ? (
        <p>No one gains or loses media.</p>
      ) : (
        changes
          .filter((item) => item.gained.length || item.lost.length)
          .map((item) => (
            <details key={item.person}>
              <summary>
                <strong>{item.person}</strong>
                <span>
                  {item.gained.length > 0 &&
                    `Gains ${item.gained.length} items`}
                  {item.gained.length > 0 && item.lost.length > 0 && ", "}
                  {item.lost.length > 0 && `Loses ${item.lost.length} items`}
                </span>
              </summary>
              {item.gained.length > 0 && <p>Gains: {names(item.gained)}</p>}
              {item.lost.length > 0 && <p>Loses: {names(item.lost)}</p>}
            </details>
          ))
      )}
      {covers.map((item) => (
        <p key={item.person}>
          <strong>{item.person}'s album cover changes.</strong>{" "}
          {item.before?.label ?? "No cover"} to{" "}
          {item.after?.label ?? "a neutral placeholder"}.
        </p>
      ))}
      {!before.published && (
        <p className="curator-hint">
          This album is unpublished. These changes apply to the view after
          publication.
        </p>
      )}
    </section>
  );
}

export function AccessEditor({
  study,
  moment,
  entry,
  onSave,
  onCancel,
  onDirty,
}: {
  study: Study;
  moment: Moment;
  entry?: Entry;
  onSave: (study: Study, message: string) => void;
  onCancel: () => void;
  onDirty: () => void;
}) {
  const [draft, setDraft] = useState<Decisions>(() =>
    entry ? { ...entry.decisions } : accessDraft(study, moment),
  );
  const detected = detectedPeople(study, moment.id);
  const after = entry
    ? {
        ...study,
        entries: study.entries.map((item) =>
          item.id === entry.id ? { ...item, decisions: draft } : item,
        ),
      }
    : {
        ...study,
        moments: study.moments.map((item) =>
          item.id === moment.id ? { ...item, decisions: draft } : item,
        ),
      };
  const entries = entry
    ? [entry]
    : study.entries.filter((item) => item.moment === moment.id);
  return (
    <form
      className="curator-form"
      onChange={onDirty}
      onSubmit={(event) => {
        event.preventDefault();
        onSave(after, entry ? "Item access saved." : "Moment access saved.");
      }}
    >
      <h3>{entry ? "Edit item access" : "Edit Moment access"}</h3>
      <p className="curator-hint">
        {entry
          ? "An item decision overrides both Moment and Album access."
          : "Detected people are suggested below. Nothing changes until you save."}
      </p>
      {!entry && (
        <p className="curator-hint">
          Fictional face associations. No Immich connection.
        </p>
      )}
      {people.map((person) => {
        const saved = (entry?.decisions ?? moment.decisions)[person];
        const inherited = access(
          study,
          { ...entries[0], decisions: {}, ...(entry ? {} : { moment: "" }) },
          person,
        );
        const updated = after.entries.filter((item) =>
          entries.some((original) => original.id === item.id),
        );
        const count = updated.filter(
          (item) => access(after, item, person).allowed,
        ).length;
        return (
          <div className="decision-row" key={person}>
            <label htmlFor={`decision-${person}`}>
              <strong>{person}</strong>
              {!entry && (
                <small>
                  {detected.includes(person)
                    ? saved === "deny"
                      ? "Detected, explicitly excluded"
                      : pendingRecommendations(study, moment).includes(person)
                        ? "Detected, suggested"
                        : "Detected in this Moment"
                    : saved === "allow"
                      ? "Saved inclusion, no longer detected"
                      : "Not detected"}
                </small>
              )}
              <small>
                {count} of {entries.length} items after saving
              </small>
            </label>
            <select
              id={`decision-${person}`}
              onChange={(event) =>
                setDraft({ ...draft, [person]: event.target.value as Decision })
              }
              value={draft[person] ?? "inherit"}
            >
              <option value="inherit">
                Inherit: {inherited.allowed ? "allowed" : "denied"}
              </option>
              <option value="allow">Allow</option>
              <option value="deny">Deny</option>
            </select>
          </div>
        );
      })}
      <ChangeReview after={after} before={study} />
      <div className="curator-buttons">
        <button className="action-button" type="submit">
          Save access
        </button>
        <button className="curator-button" onClick={onCancel} type="button">
          Cancel
        </button>
      </div>
    </form>
  );
}

export function StructureEditor({
  study,
  moment,
  selected,
  operation,
  onSave,
  onClose,
  onDirty,
}: {
  study: Study;
  moment: Moment;
  selected: string[];
  operation: "move" | "split" | "merge";
  onSave: (study: Study, message: string) => void;
  onClose: () => void;
  onDirty: () => void;
}) {
  const others = study.moments.filter((item) => item.id !== moment.id);
  const [targetId, setTargetId] = useState(others[0]?.id ?? "");
  const [name, setName] = useState("");
  const [cover, setCover] = useState("");
  const [replacement, setReplacement] = useState("");
  const [combined, setCombined] = useState<Decisions>({});
  const target = others.find((item) => item.id === targetId);
  const sourceEntries = study.entries.filter(
    (entry) => entry.moment === moment.id,
  );
  const chosen = sourceEntries.filter((entry) => selected.includes(entry.id));
  const remaining = sourceEntries.filter(
    (entry) => !selected.includes(entry.id),
  );
  const movingCover = selected.includes(moment.cover);
  const mergeEntries = study.entries.filter(
    (entry) => entry.moment === moment.id || entry.moment === targetId,
  );
  const conflict = people.some(
    (person) =>
      (moment.decisions[person] ?? "inherit") !==
      (target?.decisions[person] ?? "inherit"),
  );
  const [splitId] = useState(() => `split-${crypto.randomUUID()}`);
  const replacementNeeded = operation !== "merge" && movingCover;
  const coverOptions = operation === "merge" ? mergeEntries : chosen;
  const valid =
    (operation === "split" || !!target) &&
    (operation === "merge" || (chosen.length > 0 && remaining.length > 0)) &&
    (!replacementNeeded ||
      remaining.some((entry) => entry.id === replacement)) &&
    (operation === "move" ||
      coverOptions.some((entry) => entry.id === cover)) &&
    (operation !== "merge" ||
      !conflict ||
      people.every((person) => combined[person]));
  let after = study;
  if (valid) {
    if (operation === "merge" && target) {
      after = {
        ...study,
        moments: study.moments
          .filter((item) => item.id !== moment.id)
          .map((item) =>
            item.id === targetId
              ? {
                  ...item,
                  title: name.trim() || target.title,
                  cover,
                  decisions: conflict ? combined : target.decisions,
                }
              : item,
          ),
        entries: study.entries.map((entry) =>
          entry.moment === moment.id ? { ...entry, moment: targetId } : entry,
        ),
      };
    } else {
      const destination = operation === "split" ? splitId : targetId;
      after = {
        ...study,
        moments: study.moments.map((item) =>
          item.id === moment.id && movingCover
            ? { ...item, cover: replacement }
            : item,
        ),
        entries: study.entries.map((entry) =>
          selected.includes(entry.id)
            ? { ...entry, moment: destination }
            : entry,
        ),
      };
      if (operation === "split")
        after.moments = [
          ...after.moments,
          {
            id: splitId,
            title: name.trim() || `${dateRange(chosen)} (2)`,
            cover,
            decisions: { ...moment.decisions },
          },
        ];
    }
  }
  return (
    <StudyDialog
      onClose={onClose}
      title={
        operation === "move"
          ? "Move media"
          : operation === "split"
            ? "Split Moment"
            : "Merge Moments"
      }
    >
      <form
        className="curator-form"
        onChange={onDirty}
        onSubmit={(event) => {
          event.preventDefault();
          if (valid)
            onSave(
              after,
              operation === "move"
                ? "Media moved. Recommendations updated."
                : operation === "split"
                  ? "Moment split. Access decisions copied to both Moments."
                  : "Moments merged with your chosen access.",
            );
        }}
      >
        <p>
          {operation === "merge"
            ? `Merge all ${sourceEntries.length} items in ${moment.title} into another Moment.`
            : `${chosen.length} items selected from ${moment.title}. Item overrides stay with the media.`}
        </p>
        {operation !== "split" && (
          <label>
            Destination Moment
            <select
              onChange={(event) => {
                setTargetId(event.target.value);
                setCover("");
                setCombined({});
              }}
              required
              value={targetId}
            >
              {others.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.title}
                </option>
              ))}
            </select>
          </label>
        )}
        {operation !== "move" && (
          <label>
            {operation === "merge" ? "Merged Moment title" : "New Moment title"}
            <input
              onChange={(event) => setName(event.target.value)}
              placeholder={
                operation === "merge"
                  ? target?.title
                  : `${dateRange(chosen)} (2)`
              }
              value={name}
            />
          </label>
        )}
        {operation === "split" && (
          <p className="curator-hint">
            Both Moments keep the original Moment's access decisions. Splitting
            alone changes no one's media access.
          </p>
        )}
        {operation === "merge" && conflict && (
          <fieldset>
            <legend>Choose the combined audience</legend>
            <p className="curator-hint">
              These Moments have different decisions. Choose each person's
              access explicitly. Item overrides remain unchanged.
            </p>
            {people.map((person) => (
              <label className="decision-row" key={person}>
                <span>
                  {person}
                  <small>
                    {moment.title}: {moment.decisions[person] ?? "inherit"}
                    <br />
                    {target?.title}: {target?.decisions[person] ?? "inherit"}
                  </small>
                </span>
                <select
                  onChange={(event) =>
                    setCombined({
                      ...combined,
                      [person]: event.target.value as Decision,
                    })
                  }
                  required
                  value={combined[person] ?? ""}
                >
                  <option disabled value="">
                    Choose access
                  </option>
                  <option value="inherit">Inherit Album access</option>
                  <option value="allow">Allow</option>
                  <option value="deny">Deny</option>
                </select>
              </label>
            ))}
          </fieldset>
        )}
        {operation !== "move" && (
          <label>
            {operation === "merge" ? "Merged Moment cover" : "New Moment cover"}
            <select
              onChange={(event) => setCover(event.target.value)}
              required
              value={cover}
            >
              <option disabled value="">
                Choose a cover
              </option>
              {coverOptions.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.label}
                </option>
              ))}
            </select>
          </label>
        )}
        {replacementNeeded && (
          <label>
            Replacement cover for {moment.title}
            <span className="curator-hint">
              The current cover is moving. Choose a cover from the remaining
              media.
            </span>
            <select
              onChange={(event) => setReplacement(event.target.value)}
              required
              value={replacement}
            >
              <option disabled value="">
                Choose a cover
              </option>
              {remaining.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.label}
                </option>
              ))}
            </select>
          </label>
        )}
        {operation !== "merge" && remaining.length === 0 && (
          <p role="alert">
            Leave at least one item in this Moment, or use Merge Moments
            instead.
          </p>
        )}
        {valid ? (
          <ChangeReview after={after} before={study} />
        ) : (
          <p className="curator-hint">
            Complete the choices above to review visibility before saving.
          </p>
        )}
        <div className="curator-buttons">
          <button className="action-button" disabled={!valid} type="submit">
            {operation === "move"
              ? "Confirm move"
              : operation === "split"
                ? "Confirm split"
                : "Confirm merge"}
          </button>
          <button className="curator-button" onClick={onClose} type="button">
            Cancel
          </button>
        </div>
      </form>
    </StudyDialog>
  );
}

export function AlbumAccess({
  study,
  onSave,
  onDirty,
  onDiscard,
}: {
  study: Study;
  onSave: (study: Study, message: string) => void;
  onDirty: () => void;
  onDiscard: () => void;
}) {
  const [draft, setDraft] = useState({ ...study.albumAccess });
  const [remove, setRemove] = useState<string | null>(null);
  const after = { ...study, albumAccess: draft };
  const revoke = remove
    ? {
        ...study,
        albumAccess: { ...study.albumAccess, [remove]: "inherit" as const },
        moments: study.moments.map((moment) => ({
          ...moment,
          decisions: { ...moment.decisions, [remove]: "inherit" as const },
        })),
        entries: study.entries.map((entry) => ({
          ...entry,
          decisions: { ...entry.decisions, [remove]: "inherit" as const },
        })),
      }
    : study;
  return (
    <section className="curator-section">
      <h2>Album access</h2>
      <p className="curator-hint">
        Give someone access across the album. Moment and item exceptions still
        apply.
      </p>
      <form
        className="curator-form"
        onChange={onDirty}
        onSubmit={(event) => {
          event.preventDefault();
          onSave(after, "Album-wide access saved. Narrower decisions kept.");
        }}
      >
        {people.map((person) => (
          <div className="album-person" key={person}>
            <label className="curator-check">
              <input
                checked={draft[person] === "allow"}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    [person]: event.target.checked ? "allow" : "inherit",
                  })
                }
                type="checkbox"
              />
              <span>
                <strong>{person}</strong>
                <small>
                  {visibleEntries(after, person).length} of{" "}
                  {study.entries.length} items accessible after saving
                </small>
              </span>
            </label>
            <button
              className="text-button"
              onClick={() => {
                const changed = people.some(
                  (name) =>
                    (draft[name] === "allow") !==
                    (study.albumAccess[name] === "allow"),
                );
                if (
                  changed &&
                  !window.confirm(
                    "Discard unsaved form changes before removing all access?",
                  )
                )
                  return;
                setDraft({ ...study.albumAccess });
                onDiscard();
                setRemove(person);
              }}
              type="button"
            >
              Remove all access…
            </button>
          </div>
        ))}
        <p className="curator-hint">
          Unchecking a person removes only Album-wide access. Their Moment and
          item decisions stay in place.
        </p>
        <ChangeReview after={after} before={study} />
        <button className="action-button" type="submit">
          Save Album access
        </button>
      </form>
      {remove && (
        <StudyDialog
          onClose={() => setRemove(null)}
          title={`Remove all access for ${remove}?`}
        >
          <p>
            Remove every Album, Moment, and item decision for {remove} in this
            album. Other people's access stays unchanged.
          </p>
          <ChangeReview after={revoke} before={study} />
          <div className="curator-buttons">
            <button
              className="action-button"
              onClick={() =>
                onSave(revoke, `All access removed for ${remove}.`)
              }
            >
              Confirm removal
            </button>
            <button className="curator-button" onClick={() => setRemove(null)}>
              Cancel
            </button>
          </div>
        </StudyDialog>
      )}
    </section>
  );
}
