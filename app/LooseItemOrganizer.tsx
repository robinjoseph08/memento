import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { APIError } from "./api";
import {
  looseItemKeys,
  useLooseItem,
  useLooseItems,
  usePublishLooseItem,
  useUpdateLooseItem,
  useWithdrawLooseItem,
  type LooseItemAttempt,
} from "./hooks/queries/looseItems";
import { LooseAudienceReview } from "./loose-workspace/LooseAudienceReview";
import { LooseRecipientPreview } from "./loose-workspace/LoosePreview";
import type { LooseItem } from "./types/generated/events";
import type { SessionResponse } from "./types/generated/setup";

type Pane = "work" | "organize" | "inspect";
type SaveState = "saved" | "saving" | "unsaved" | "failed" | "conflict";

function clone<T>(value: T): T {
  return structuredClone(value);
}

function validTimezone(value: string) {
  try {
    if (!value.trim() || value.trim() === "Local") return false;
    new Intl.DateTimeFormat("en-US", { timeZone: value.trim() }).format();
    return true;
  } catch {
    return false;
  }
}

function validDate(value: string | null) {
  if (value === null) return true;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return false;
  const [year, month, day] = match.slice(1).map(Number);
  const parsed = new Date(Date.UTC(year, month - 1, day));
  return (
    parsed.getUTCFullYear() === year &&
    parsed.getUTCMonth() === month - 1 &&
    parsed.getUTCDate() === day
  );
}

function validMetadata(looseItem: LooseItem | undefined) {
  return Boolean(
    looseItem &&
    validTimezone(looseItem.grouping_timezone) &&
    validDate(looseItem.proposed_day) &&
    Array.from(looseItem.title.trim()).length <= 240 &&
    Array.from(looseItem.description.trim()).length <= 2000 &&
    looseItem.place_labels.length <= 20 &&
    looseItem.place_labels.every((label) => {
      const length = Array.from(label.trim()).length;
      return length >= 1 && length <= 120;
    }),
  );
}

function overlayLocalFields(local: LooseItem, authoritative: LooseItem) {
  return {
    ...clone(authoritative),
    title: local.title,
    description: local.description,
    grouping_timezone: local.grouping_timezone,
    proposed_day: local.proposed_day,
    place_labels: clone(local.place_labels),
  };
}

export function LooseItemOrganizer({
  session,
  onDirtyChange,
  onSavingChange,
}: {
  session: SessionResponse;
  onDirtyChange?: (dirty: boolean) => void;
  onSavingChange?: (saving: boolean) => void;
}) {
  const identityGeneration = session.csrf_token;
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const looseKindRequested = searchParams.has("loose");
  const requestedID = searchParams.get("loose") ?? "";
  const [selectedID, setSelectedID] = useState(requestedID);
  const [draft, setDraft] = useState<LooseItem>();
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [revision, setRevision] = useState(0);
  const [selectionGeneration, setSelectionGeneration] = useState(0);
  const [activePane, setActivePane] = useState<Pane>(() =>
    requestedID ? "organize" : "work",
  );
  const [previewReset, setPreviewReset] = useState(0);
  const [publicationPending, setPublicationPending] = useState(false);
  const [withdrawalPending, setWithdrawalPending] = useState(false);
  const [authorityRequired, setAuthorityRequired] = useState(false);
  const [authorityError, setAuthorityError] = useState("");
  const [operationStatus, setOperationStatus] = useState("");
  const [notifyRecipients, setNotifyRecipients] = useState(true);
  const selectedIDRef = useRef(selectedID);
  const selectionGenerationRef = useRef(0);
  const revisionRef = useRef(0);
  const latestDraftRef = useRef<LooseItem | undefined>(undefined);
  const authorityAttemptRef = useRef<LooseItemAttempt | undefined>(undefined);
  const list = useLooseItems(identityGeneration);
  const detail = useLooseItem(identityGeneration, selectedID);
  const server = detail.data?.id === selectedID ? detail.data : undefined;
  const local = draft?.id === selectedID ? draft : undefined;
  const current = saveState === "saved" ? (server ?? local) : (local ?? server);
  const metadataValid = validMetadata(current);

  function ownsAttempt(attempt: LooseItemAttempt) {
    return (
      selectedIDRef.current === attempt.looseItem.id &&
      selectionGenerationRef.current === attempt.selectionGeneration
    );
  }

  const save = useUpdateLooseItem(identityGeneration, {
    onMutate: () => setSaveState("saving"),
    onSuccess: (saved, attempt) => {
      if (!ownsAttempt(attempt)) return;
      const latest = latestDraftRef.current;
      const newerEdits = revisionRef.current > attempt.revision;
      const next =
        latest?.id === saved.id && newerEdits
          ? overlayLocalFields(latest, saved)
          : clone(saved);
      latestDraftRef.current = next;
      setDraft(next);
      if (newerEdits) setSaveState("unsaved");
      else {
        revisionRef.current = 0;
        setRevision(0);
        setSaveState("saved");
      }
    },
    onError: (error, attempt) => {
      if (!ownsAttempt(attempt)) return;
      setSaveState(
        error instanceof APIError && error.status === 409
          ? "conflict"
          : "failed",
      );
    },
  });

  async function recoverAuthority(attempt = authorityAttemptRef.current) {
    const looseItemID = attempt?.looseItem.id ?? selectedIDRef.current;
    const generation =
      attempt?.selectionGeneration ?? selectionGenerationRef.current;
    if (
      !looseItemID ||
      selectedIDRef.current !== looseItemID ||
      selectionGenerationRef.current !== generation
    )
      return;
    setAuthorityRequired(true);
    setAuthorityError("");
    const result = await detail.refetchAuthoritative();
    if (
      selectedIDRef.current !== looseItemID ||
      selectionGenerationRef.current !== generation
    )
      return;
    if (!result.isSuccess || !result.data) {
      setAuthorityError(
        result.error?.message ??
          "The authoritative Loose item could not be loaded.",
      );
      return;
    }
    const latest = latestDraftRef.current;
    const keepEdits =
      latest?.id === looseItemID &&
      attempt !== undefined &&
      revisionRef.current > attempt.revision;
    const next = keepEdits
      ? overlayLocalFields(latest, result.data)
      : clone(result.data);
    latestDraftRef.current = next;
    setDraft(next);
    if (keepEdits) setSaveState("unsaved");
    else {
      revisionRef.current = 0;
      setRevision(0);
      setSaveState("saved");
    }
    authorityAttemptRef.current = undefined;
    setAuthorityRequired(false);
  }

  const publish = usePublishLooseItem(identityGeneration, {
    onStarted: (attempt) => {
      if (!ownsAttempt(attempt)) return;
      setPublicationPending(true);
      setPreviewReset((value) => value + 1);
      setOperationStatus("");
    },
    onCommitted: (publication, attempt) => {
      if (!ownsAttempt(attempt)) return;
      setOperationStatus(
        `Published Loose item revision ${publication.revision}.`,
      );
      setAuthorityRequired(true);
    },
    onSuccess: (_publication, attempt, authoritative) => {
      if (!ownsAttempt(attempt)) return;
      setPublicationPending(false);
      if (authoritative) {
        latestDraftRef.current = authoritative;
        setDraft(authoritative);
        setAuthorityRequired(false);
        setAuthorityError("");
      } else {
        authorityAttemptRef.current = attempt;
        void recoverAuthority(attempt);
      }
    },
    onError: (_error, attempt) => {
      if (!ownsAttempt(attempt)) return;
      setPublicationPending(false);
      authorityAttemptRef.current = attempt;
      void recoverAuthority(attempt);
    },
  });

  const withdraw = useWithdrawLooseItem(identityGeneration, {
    onStarted: (attempt) => {
      if (!ownsAttempt(attempt)) return;
      setWithdrawalPending(true);
      setPreviewReset((value) => value + 1);
      setOperationStatus("");
    },
    onCommitted: (result, attempt) => {
      if (!ownsAttempt(attempt)) return;
      setOperationStatus(
        `Access withdrawn immediately for ${result.affected_recipient_count} Recipients. Fresh Audience review and Publication are required to restore it.`,
      );
      setAuthorityRequired(true);
    },
    onSuccess: (_result, attempt, authoritative) => {
      if (!ownsAttempt(attempt)) return;
      setWithdrawalPending(false);
      if (authoritative) {
        latestDraftRef.current = authoritative;
        setDraft(authoritative);
        setAuthorityRequired(false);
        setAuthorityError("");
      } else {
        authorityAttemptRef.current = attempt;
        void recoverAuthority(attempt);
      }
    },
    onError: (_error, attempt) => {
      if (!ownsAttempt(attempt)) return;
      setWithdrawalPending(false);
      authorityAttemptRef.current = attempt;
      void recoverAuthority(attempt);
    },
  });

  function resetSelection(nextID: string) {
    const previousID = selectedIDRef.current;
    if (previousID)
      void queryClient.cancelQueries({
        queryKey: looseItemKeys.detail(identityGeneration, previousID),
        exact: true,
      });
    selectionGenerationRef.current += 1;
    setSelectionGeneration(selectionGenerationRef.current);
    selectedIDRef.current = nextID;
    setSelectedID(nextID);
    latestDraftRef.current = undefined;
    authorityAttemptRef.current = undefined;
    revisionRef.current = 0;
    setRevision(0);
    setDraft(undefined);
    setSaveState("saved");
    setAuthorityRequired(false);
    setAuthorityError("");
    setOperationStatus("");
    setPublicationPending(false);
    setWithdrawalPending(false);
    setNotifyRecipients(true);
    setPreviewReset(0);
    save.reset();
    publish.reset();
    withdraw.reset();
    setActivePane(nextID ? "organize" : "work");
  }

  function select(nextID: string) {
    if (nextID === selectedID) return;
    if (
      saveState !== "saved" &&
      !window.confirm("Discard changes that have not finished saving?")
    )
      return;
    resetSelection(nextID);
    setSearchParams((currentParams) => {
      const next = new URLSearchParams(currentParams);
      next.delete("event");
      next.set("loose", nextID);
      next.set("workspace", "drafts");
      return next;
    });
  }

  const synchronizeURL = useEffectEvent((nextID: string) => {
    if (
      publicationPending ||
      withdrawalPending ||
      save.isPending ||
      authorityRequired
    ) {
      setSearchParams(
        (currentParams) => {
          const next = new URLSearchParams(currentParams);
          if (selectedIDRef.current) next.set("loose", selectedIDRef.current);
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
        (currentParams) => {
          const next = new URLSearchParams(currentParams);
          if (selectedIDRef.current) next.set("loose", selectedIDRef.current);
          return next;
        },
        { replace: true },
      );
      return;
    }
    resetSelection(nextID);
  });

  useEffect(() => {
    if (looseKindRequested && requestedID !== selectedIDRef.current)
      synchronizeURL(requestedID);
  }, [looseKindRequested, requestedID]);

  useEffect(() => {
    if (saveState === "saved" && server) latestDraftRef.current = server;
  }, [saveState, server]);

  useEffect(() => {
    const dirty = saveState !== "saved";
    const busy =
      save.isPending ||
      publicationPending ||
      withdrawalPending ||
      authorityRequired;
    onDirtyChange?.(dirty);
    onSavingChange?.(busy);
    const preventUnload = (event: BeforeUnloadEvent) => {
      if (!dirty && !busy) return;
      event.preventDefault();
    };
    window.addEventListener("beforeunload", preventUnload);
    return () => window.removeEventListener("beforeunload", preventUnload);
  }, [
    authorityRequired,
    onDirtyChange,
    onSavingChange,
    publicationPending,
    save.isPending,
    saveState,
    withdrawalPending,
  ]);

  const mutateSave = save.mutate;
  useEffect(() => {
    if (
      !current ||
      revision === 0 ||
      !metadataValid ||
      save.isPending ||
      saveState === "failed" ||
      saveState === "conflict" ||
      publicationPending ||
      withdrawalPending ||
      authorityRequired
    )
      return;
    const timer = window.setTimeout(() => {
      mutateSave({
        looseItem: clone(current),
        revision: revisionRef.current,
        selectionGeneration: selectionGenerationRef.current,
      });
    }, 450);
    return () => window.clearTimeout(timer);
  }, [
    authorityRequired,
    current,
    metadataValid,
    mutateSave,
    publicationPending,
    revision,
    save.isPending,
    saveState,
    withdrawalPending,
  ]);

  function change(mutator: (next: LooseItem) => void) {
    if (!current) return;
    const next = clone(current);
    mutator(next);
    const nextRevision = revisionRef.current + 1;
    revisionRef.current = nextRevision;
    latestDraftRef.current = next;
    setDraft(next);
    setRevision(nextRevision);
    setSaveState("unsaved");
    setPreviewReset((value) => value + 1);
  }

  async function resolveConflict(keepMine: boolean) {
    const looseItemID = selectedIDRef.current;
    const generation = selectionGenerationRef.current;
    const localDraft = latestDraftRef.current;
    const result = await detail.refetchAuthoritative();
    if (
      selectedIDRef.current !== looseItemID ||
      selectionGenerationRef.current !== generation ||
      !result.isSuccess ||
      !result.data
    )
      return;
    const next =
      keepMine && localDraft
        ? overlayLocalFields(localDraft, result.data)
        : clone(result.data);
    latestDraftRef.current = next;
    setDraft(next);
    save.reset();
    if (keepMine) {
      const nextRevision = revisionRef.current + 1;
      revisionRef.current = nextRevision;
      setRevision(nextRevision);
      setSaveState("unsaved");
    } else {
      revisionRef.current = 0;
      setRevision(0);
      setSaveState("saved");
    }
  }

  async function reflectAudience(
    _kind: "audience-changed" | "audience-approved",
    looseItemID: string,
    generation: number,
  ) {
    if (
      selectedIDRef.current !== looseItemID ||
      selectionGenerationRef.current !== generation
    )
      return;
    setPreviewReset((value) => value + 1);
    setAuthorityRequired(true);
    const result = await detail.refetchAuthoritative();
    if (
      selectedIDRef.current !== looseItemID ||
      selectionGenerationRef.current !== generation
    )
      return;
    if (!result.isSuccess || !result.data) {
      setAuthorityError(
        result.error?.message ??
          "The authoritative Loose item could not be loaded.",
      );
      return;
    }
    latestDraftRef.current = result.data;
    setDraft(result.data);
    setSaveState("saved");
    revisionRef.current = 0;
    setRevision(0);
    setAuthorityRequired(false);
  }

  const controlsReady =
    saveState === "saved" &&
    !authorityRequired &&
    !publicationPending &&
    !withdrawalPending;
  const canPublish =
    Boolean(current?.audience_complete) &&
    controlsReady &&
    (current?.version !== current?.published_editable_version ||
      Boolean(current?.pending_withdrawal_publication));
  const correctionPrivate = Boolean(current?.has_staged_update);
  const looseWithdrawalTarget = current?.withdrawal_targets.find(
    (target) =>
      target.target_kind === "loose_item" && target.target_id === current.id,
  );

  return (
    <section aria-labelledby="loose-work-title" className="curator-workspace">
      <header className="work-header">
        <div>
          <p className="step-label">Curator Loose workspace</p>
          <h2 id="loose-work-title">Curate Loose items</h2>
        </div>
        <p aria-live="polite" className={`save-state ${saveState}`}>
          {!metadataValid
            ? "Fix validation errors before autosave"
            : saveState === "saved"
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
      {operationStatus ? (
        <p className="workspace-status" role="status">
          {operationStatus}
        </p>
      ) : null}
      {correctionPrivate ? (
        <p className="workspace-status">
          This correction remains private. Recipients continue to see the
          current Publication until you publish the correction.
        </p>
      ) : null}
      {authorityRequired ? (
        <div className="form-error" role="alert">
          <p>
            Reload the authoritative Loose item before Preview, Withdrawal, or
            Publication can continue.
          </p>
          {authorityError ? <p>{authorityError}</p> : null}
          <button
            disabled={!authorityError}
            onClick={() => void recoverAuthority()}
            type="button"
          >
            {authorityError
              ? "Retry Loose item reload"
              : "Reloading Loose item…"}
          </button>
        </div>
      ) : null}
      {saveState === "conflict" ? (
        <div className="conflict" role="alert">
          <strong>This Loose item changed in another browser.</strong>
          <p>Your edits have not overwritten the newer version.</p>
          <button onClick={() => void resolveConflict(false)} type="button">
            Load newer version
          </button>
          <button
            disabled={!metadataValid}
            onClick={() => void resolveConflict(true)}
            type="button"
          >
            Keep my changes
          </button>
        </div>
      ) : save.isError ? (
        <div className="form-error" role="alert">
          <p>{save.error.message}</p>
          <button
            disabled={!current || !metadataValid}
            onClick={() =>
              current &&
              save.mutate({
                looseItem: clone(current),
                revision: revisionRef.current,
                selectionGeneration: selectionGenerationRef.current,
              })
            }
            type="button"
          >
            Retry autosave
          </button>
        </div>
      ) : null}
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
                ? "Loose item"
                : "Review"}
          </button>
        ))}
      </nav>
      <fieldset
        aria-label="Curator Loose item workspace"
        className="curator-split"
        data-active-pane={activePane}
        disabled={authorityRequired || publicationPending || withdrawalPending}
      >
        <aside className="work-pane">
          <h3>Loose item work</h3>
          {list.isPending ? <p>Loading Loose items…</p> : null}
          {list.isError ? (
            <p className="form-error" role="alert">
              {list.error.message}
            </p>
          ) : null}
          {list.data?.loose_items.length === 0 ? (
            <p>No Loose items yet.</p>
          ) : null}
          <ul className="event-list">
            {list.data?.loose_items.map((item) => (
              <li key={item.id}>
                <button
                  aria-current={selectedID === item.id ? "page" : undefined}
                  disabled={
                    item.id !== selectedID &&
                    (save.isPending || publicationPending || withdrawalPending)
                  }
                  onClick={() => select(item.id)}
                  type="button"
                >
                  <strong>{item.title || "Untitled Loose item"}</strong>
                  <span>
                    {item.has_staged_update
                      ? "Private correction"
                      : item.lifecycle === "published"
                        ? "Published"
                        : "Draft"}{" "}
                    ·{" "}
                    {item.audience_complete
                      ? "Audience reviewed"
                      : "Audience review needed"}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </aside>
        <section
          aria-label="Active Loose item organization"
          className="organize-pane"
        >
          {!selectedID ? (
            <p className="pane-empty">Choose a Loose item from Work.</p>
          ) : null}
          {detail.isPending && selectedID ? <p>Loading Loose item…</p> : null}
          {detail.isError && selectedID ? (
            <p className="form-error" role="alert">
              {detail.error.message}
            </p>
          ) : null}
          {current ? (
            <>
              <header>
                <div>
                  <p className="step-label">
                    {current.lifecycle === "published"
                      ? "Published Loose item"
                      : "Private Loose item draft"}
                  </p>
                  <h3>{current.title || "Untitled Loose item"}</h3>
                  <p>
                    One independent Media item · {current.media_item.media_type}
                  </p>
                </div>
                <section
                  aria-labelledby="loose-readiness-title"
                  className="readiness"
                >
                  <h3 id="loose-readiness-title">Readiness</h3>
                  <ul>
                    <li>{metadataValid ? "✓" : "○"} Details</li>
                    <li>{current.audience_complete ? "✓" : "○"} Audience</li>
                  </ul>
                  <p>
                    <strong>Next action:</strong>{" "}
                    {canPublish
                      ? current.pending_withdrawal_publication
                        ? "Publish Withdrawal restoration"
                        : "Ready to publish"
                      : current.audience_complete
                        ? "Save details"
                        : current.pending_withdrawal_publication
                          ? "Fresh Audience review"
                          : "Audience review"}
                  </p>
                </section>
              </header>
              <section
                aria-labelledby="loose-details-title"
                className="event-details-editor"
              >
                <h4 id="loose-details-title">Loose item details</h4>
                <label>
                  Loose item title
                  <input
                    maxLength={240}
                    onChange={(event) =>
                      change((next) => {
                        next.title = event.target.value;
                      })
                    }
                    type="text"
                    value={current.title}
                  />
                </label>
                <label>
                  Description
                  <textarea
                    maxLength={2000}
                    onChange={(event) =>
                      change((next) => {
                        next.description = event.target.value;
                      })
                    }
                    value={current.description}
                  />
                </label>
                <label>
                  Place labels
                  <input
                    aria-label="Loose item Place labels"
                    onChange={(event) =>
                      change((next) => {
                        next.place_labels = event.target.value
                          .split(",")
                          .map((label) => label.trim())
                          .filter(Boolean);
                      })
                    }
                    type="text"
                    value={current.place_labels.join(", ")}
                  />
                </label>
                <label>
                  Date
                  <input
                    aria-label="Loose item date"
                    onChange={(event) =>
                      change((next) => {
                        next.proposed_day = event.target.value || null;
                      })
                    }
                    type="date"
                    value={current.proposed_day ?? ""}
                  />
                </label>
                <label>
                  Timezone
                  <input
                    aria-invalid={!metadataValid}
                    aria-label="Loose item timezone"
                    onChange={(event) =>
                      change((next) => {
                        next.grouping_timezone = event.target.value;
                      })
                    }
                    type="text"
                    value={current.grouping_timezone}
                  />
                </label>
                {!metadataValid ? (
                  <p className="form-field-error">
                    Enter a valid IANA timezone, such as America/New_York or
                    UTC.
                  </p>
                ) : null}
              </section>
              <section
                aria-labelledby="loose-media-title"
                className="moment-card"
              >
                <h4 id="loose-media-title">Media</h4>
                <p>
                  {current.media_item.media_type} · {current.media_item.id}
                </p>
                <p>
                  This one Media identity is shared independently, without Event
                  or Moment semantics.
                </p>
              </section>
            </>
          ) : null}
        </section>
        <aside className="inspect-pane">
          {current ? (
            <>
              <LooseAudienceReview
                key={`audience:${current.id}`}
                disabled={!controlsReady}
                identityGeneration={identityGeneration}
                looseItemID={current.id}
                onReviewChanged={(...args) => void reflectAudience(...args)}
                selectionGeneration={selectionGeneration}
              />
              <LooseRecipientPreview
                key={`preview:${current.id}`}
                controlsReady={controlsReady}
                identityGeneration={identityGeneration}
                looseItem={current}
                resetKey={`${current.id}:${current.version}:${revision}:${previewReset}`}
              />
              {looseWithdrawalTarget || current.withdrawals.length ? (
                <section
                  aria-labelledby="loose-withdraw-title"
                  className="publication-actions"
                >
                  <h4 id="loose-withdraw-title">Withdraw access</h4>
                  <p>
                    Withdrawal takes effect immediately. Restoration requires a
                    freshly reviewed Audience and a later Publication.
                  </p>
                  {looseWithdrawalTarget ? (
                    <LooseWithdrawalButton
                      key={`withdrawal:${current.id}`}
                      current={current}
                      controlsReady={controlsReady}
                      revision={revision}
                      selectionGeneration={selectionGeneration}
                      withdraw={withdraw}
                    />
                  ) : (
                    <p>
                      No currently published Loose item target is available to
                      withdraw.
                    </p>
                  )}
                  {current.withdrawals.length ? (
                    <div>
                      <h5>Withdrawal history</h5>
                      <ul>
                        {current.withdrawals.map((item) => (
                          <li key={item.id}>
                            {item.reason} by {item.withdrawn_by_name}.{" "}
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
                aria-labelledby="loose-publication-title"
                className="publication-actions"
              >
                <h4 id="loose-publication-title">Publication</h4>
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
                  disabled={!canPublish}
                  onClick={() => {
                    publish.mutate({
                      looseItem: current,
                      revision,
                      selectionGeneration: selectionGenerationRef.current,
                      notifyRecipients,
                    });
                  }}
                  type="button"
                >
                  {publish.isPending
                    ? "Publishing…"
                    : current.pending_withdrawal_publication
                      ? "Publish Loose item restoration"
                      : current.has_staged_update
                        ? "Publish Loose item correction"
                        : "Publish Loose item"}
                </button>
                {publish.isError ? (
                  <p className="form-error" role="alert">
                    {publish.error.message}
                  </p>
                ) : null}
              </section>
            </>
          ) : (
            <p>Choose a Loose item to review.</p>
          )}
        </aside>
      </fieldset>
    </section>
  );
}

function LooseWithdrawalButton({
  current,
  controlsReady,
  revision,
  selectionGeneration,
  withdraw,
}: {
  current: LooseItem;
  controlsReady: boolean;
  revision: number;
  selectionGeneration: number;
  withdraw: ReturnType<typeof useWithdrawLooseItem>;
}) {
  const [reason, setReason] = useState("");
  return (
    <>
      <label>
        Attributable reason
        <textarea
          maxLength={1000}
          onChange={(event) => setReason(event.target.value)}
          required
          value={reason}
        />
      </label>
      <button
        disabled={!controlsReady || withdraw.isPending || !reason.trim()}
        onClick={() => {
          if (
            window.confirm(
              "Withdraw Recipient access to this Loose item immediately? Identity and history will be preserved.",
            )
          )
            withdraw.mutate({
              looseItem: current,
              reason,
              revision,
              selectionGeneration,
            });
        }}
        type="button"
      >
        {withdraw.isPending ? "Withdrawing…" : "Withdraw Loose item access"}
      </button>
      {withdraw.isError ? (
        <p className="form-error" role="alert">
          {withdraw.error.message}
        </p>
      ) : null}
    </>
  );
}
