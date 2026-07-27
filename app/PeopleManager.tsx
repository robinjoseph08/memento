import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import { apiJSON, apiNoContent } from "./api";
import type {
  CreateRequest,
  ListResponse,
  MergePreview,
  MergeRequest,
  Person,
  UpdateRequest,
} from "./types/generated/people";
import type {
  DeliveryStatus,
  DesignateRequest,
  Invitation,
  InvitationActionRequest,
  Recipient,
} from "./types/generated/recipients";
import type { SessionResponse } from "./types/generated/setup";
import type {
  ListResponse as SessionListResponse,
  RecoveryStartResponse,
} from "./types/generated/sessions";

function ErrorNotice({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

function personOptionLabel(person: Person) {
  const details = [
    person.sort_name,
    person.status,
    person.current_login_email,
    person.id.slice(0, 8),
  ].filter(Boolean);
  return `${person.display_name} (${details.join(" · ")})`;
}

export function PeopleManager({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [includeArchived, setIncludeArchived] = useState(false);
  const [selectedID, setSelectedID] = useState("");
  const [newName, setNewName] = useState("");
  const [newSortName, setNewSortName] = useState("");
  const [mergeSourceID, setMergeSourceID] = useState("");
  const [mergeSurvivorID, setMergeSurvivorID] = useState("");
  const [preview, setPreview] = useState<MergePreview>();
  const [transferGeneration, setTransferGeneration] = useState(false);
  const [emailResolution, setEmailResolution] = useState("");

  const people = useQuery({
    queryKey: ["people", search, includeArchived],
    queryFn: () =>
      apiJSON<ListResponse>(
        `/api/people?query=${encodeURIComponent(search)}&include_archived=${includeArchived}`,
      ),
  });
  const selected = people.data?.people.find(
    (person) => person.id === selectedID,
  );

  async function refreshPeople() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["people"] }),
      queryClient.invalidateQueries({ queryKey: ["family-people"] }),
      queryClient.invalidateQueries({ queryKey: ["family-relationships"] }),
      queryClient.invalidateQueries({ queryKey: ["family-branch"] }),
      queryClient.invalidateQueries({ queryKey: ["visibility-people"] }),
      queryClient.invalidateQueries({ queryKey: ["visibility-circles"] }),
      queryClient.invalidateQueries({ queryKey: ["curator-interest-list"] }),
      queryClient.invalidateQueries({ queryKey: ["curator-discoverable"] }),
    ]);
  }

  function clearMergeSelection() {
    setMergeSourceID("");
    setMergeSurvivorID("");
    setPreview(undefined);
    setTransferGeneration(false);
    setEmailResolution("");
  }

  const createPerson = useMutation({
    mutationFn: (request: CreateRequest) =>
      apiJSON<Person>("/api/people", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: async (person) => {
      setNewName("");
      setNewSortName("");
      setSelectedID(person.id);
      await refreshPeople();
    },
  });
  const updatePerson = useMutation({
    mutationFn: ({ id, request }: { id: string; request: UpdateRequest }) =>
      apiJSON<Person>(`/api/people/${id}`, {
        method: "PATCH",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: refreshPeople,
  });
  const archivePerson = useMutation({
    mutationFn: (person: Person) =>
      apiJSON<Person>(`/api/people/${person.id}/archive`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ version: person.version }),
      }),
    onSuccess: async () => {
      setSelectedID("");
      await refreshPeople();
    },
  });
  const previewMerge = useMutation({
    mutationFn: () =>
      apiJSON<MergePreview>("/api/people/merge-preview", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({
          source_person_id: mergeSourceID,
          survivor_person_id: mergeSurvivorID,
        }),
      }),
    onSuccess: (result) => {
      setPreview(result);
      setTransferGeneration(false);
      setEmailResolution("");
    },
  });
  const confirmMerge = useMutation({
    mutationFn: (request: MergeRequest) =>
      apiJSON<Person>("/api/people/merge", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: async (person) => {
      setPreview(undefined);
      setMergeSourceID("");
      setMergeSurvivorID("");
      setSelectedID(person.id);
      await refreshPeople();
    },
  });

  function submitCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createPerson.mutate({ display_name: newName, sort_name: newSortName });
  }

  return (
    <section aria-labelledby="people-title" className="people-shell">
      <header className="people-header">
        <div>
          <p className="eyebrow">MEMENTO CURATOR</p>
          <h1 id="people-title">People</h1>
          <p>Setup is complete. You're signed in as {session.display_name}.</p>
        </div>
        <p className="status">
          <span aria-hidden="true" className="status-dot" />
          Private directory
        </p>
      </header>

      <div className="people-toolbar">
        <label>
          Search People
          <input
            onChange={(event) => {
              setSearch(event.target.value);
              clearMergeSelection();
            }}
            placeholder="Name or sort name"
            type="search"
            value={search}
          />
        </label>
        <label className="inline-choice">
          <input
            checked={includeArchived}
            onChange={(event) => {
              setIncludeArchived(event.target.checked);
              clearMergeSelection();
            }}
            type="checkbox"
          />
          Include archived and merged People
        </label>
      </div>
      <ErrorNotice error={people.error} />

      <div className="people-layout">
        <aside className="people-directory" aria-label="People directory">
          {people.data?.people.map((person) => (
            <button
              className={
                selectedID === person.id ? "person-row selected" : "person-row"
              }
              key={person.id}
              onClick={() => setSelectedID(person.id)}
              type="button"
            >
              <strong>{person.display_name}</strong>
              <span>
                {person.status}
                {person.roles.length ? ` · ${person.roles.join(", ")}` : ""}
              </span>
            </button>
          ))}
          {people.isSuccess && people.data.people.length === 0 ? (
            <p>No People found.</p>
          ) : null}
        </aside>

        <div className="people-workspace">
          <form className="people-panel" onSubmit={submitCreate}>
            <h2>Create Person</h2>
            <label>
              Display name
              <input
                maxLength={120}
                onChange={(event) => setNewName(event.target.value)}
                required
                value={newName}
              />
            </label>
            <label>
              Sort name
              <input
                maxLength={120}
                onChange={(event) => setNewSortName(event.target.value)}
                placeholder="Defaults to display name"
                value={newSortName}
              />
            </label>
            <ErrorNotice error={createPerson.error} />
            <button disabled={createPerson.isPending} type="submit">
              Create Person
            </button>
          </form>

          {selected ? (
            <PersonDetail
              archiveError={archivePerson.error}
              key={`${selected.id}:${selected.version}`}
              archivePending={archivePerson.isPending}
              onArchive={() => {
                if (
                  window.confirm(
                    `Archive ${selected.display_name}? This cannot be undone, revokes all unrevoked Session records, removes the Person from Visibility circles, and may deactivate Interest choices.`,
                  )
                ) {
                  archivePerson.mutate(selected);
                }
              }}
              onUpdate={(request) =>
                updatePerson.mutate({ id: selected.id, request })
              }
              person={selected}
              session={session}
              updateError={updatePerson.error}
            />
          ) : (
            <div className="people-panel">
              <h2>Inspect Person</h2>
              <p>
                Choose a Person to inspect durable identity, access, Sessions,
                and attribution.
              </p>
            </div>
          )}

          <div className="people-panel merge-panel">
            <h2>Merge People safely</h2>
            <p>
              The source remains in history. Preview every authority and access
              effect before confirmation.
            </p>
            <label>
              Source Person
              <select
                onChange={(event) => {
                  setMergeSourceID(event.target.value);
                  setPreview(undefined);
                }}
                value={mergeSourceID}
              >
                <option value="">Choose source</option>
                {people.data?.people
                  .filter((person) => person.status !== "merged")
                  .map((person) => (
                    <option key={person.id} value={person.id}>
                      {personOptionLabel(person)}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              Survivor Person
              <select
                onChange={(event) => {
                  setMergeSurvivorID(event.target.value);
                  setPreview(undefined);
                }}
                value={mergeSurvivorID}
              >
                <option value="">Choose survivor</option>
                {people.data?.people
                  .filter((person) => person.status === "current")
                  .map((person) => (
                    <option key={person.id} value={person.id}>
                      {personOptionLabel(person)}
                    </option>
                  ))}
              </select>
            </label>
            <ErrorNotice error={previewMerge.error} />
            <button
              disabled={
                !mergeSourceID ||
                !mergeSurvivorID ||
                mergeSourceID === mergeSurvivorID ||
                previewMerge.isPending
              }
              onClick={() => previewMerge.mutate()}
              type="button"
            >
              Preview merge
            </button>
            {preview ? (
              <MergeConfirmation
                emailResolution={emailResolution}
                error={confirmMerge.error}
                onConfirm={() =>
                  confirmMerge.mutate({
                    source_person_id: preview.source.id,
                    survivor_person_id: preview.survivor.id,
                    source_version: preview.source.version,
                    survivor_version: preview.survivor.version,
                    transfer_current_access_generation: transferGeneration,
                    expected_recipient_generation:
                      preview.affected_references
                        .resulting_recipient_generation ?? 0,
                    preview_fingerprint: preview.preview_fingerprint,
                    email_resolution: emailResolution,
                  })
                }
                onEmailResolution={setEmailResolution}
                onTransferGeneration={setTransferGeneration}
                pending={confirmMerge.isPending}
                preview={preview}
                transferGeneration={transferGeneration}
              />
            ) : null}
          </div>
        </div>
      </div>
    </section>
  );
}

function PersonDetail({
  person,
  onUpdate,
  onArchive,
  updateError,
  archiveError,
  archivePending,
  session,
}: {
  person: Person;
  session: SessionResponse;
  onUpdate: (request: UpdateRequest) => void;
  onArchive: () => void;
  updateError: Error | null;
  archiveError: Error | null;
  archivePending: boolean;
}) {
  const [displayName, setDisplayName] = useState(person.display_name);
  const [sortName, setSortName] = useState(person.sort_name);
  return (
    <form
      className="people-panel"
      onSubmit={(event) => {
        event.preventDefault();
        onUpdate({
          display_name: displayName,
          sort_name: sortName,
          version: person.version,
        });
      }}
    >
      <h2>Inspect Person</h2>
      <p>
        <strong>Status:</strong> {person.status} · <strong>Version:</strong>{" "}
        {person.version}
      </p>
      <label>
        Display name
        <input
          disabled={person.status === "merged"}
          maxLength={120}
          onChange={(event) => setDisplayName(event.target.value)}
          required
          value={displayName}
        />
      </label>
      <label>
        Sort name
        <input
          disabled={person.status === "merged"}
          maxLength={120}
          onChange={(event) => setSortName(event.target.value)}
          required
          value={sortName}
        />
      </label>
      <dl className="person-facts">
        <div>
          <dt>Roles</dt>
          <dd>{person.roles.join(", ") || "None"}</dd>
        </div>
        <div>
          <dt>Recipient generation</dt>
          <dd>
            {person.current_recipient_access
              ? `${person.current_recipient_access.generation}, ${person.current_recipient_access.state}`
              : "None"}
          </dd>
        </div>
        <div>
          <dt>Login email</dt>
          <dd>{person.current_login_email || "None"}</dd>
        </div>
        <div>
          <dt>Unrevoked Session records</dt>
          <dd>{person.unrevoked_sessions}</dd>
        </div>
        <div>
          <dt>Historical attribution rows</dt>
          <dd>{person.historical_audit_count}</dd>
        </div>
      </dl>
      {person.merged_into_person_id ? (
        <p>Merged into Person {person.merged_into_person_id}</p>
      ) : null}
      <ErrorNotice error={updateError ?? archiveError} />
      {person.status !== "merged" ? (
        <button type="submit">Save changes</button>
      ) : null}
      {person.status === "current" && !person.roles.includes("curator") ? (
        <button
          className="danger-button"
          disabled={archivePending}
          onClick={onArchive}
          type="button"
        >
          Archive Person
        </button>
      ) : null}
      {person.status === "current" && !person.roles.includes("curator") ? (
        <RecipientControls person={person} session={session} />
      ) : null}
    </form>
  );
}

function formatInvitationDate(value: unknown) {
  if (typeof value !== "string") {
    return "Not yet";
  }
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "Unknown"
    : new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

function deliveryFailureLabel(value: string) {
  return value.replaceAll("_", " ");
}

function deliveryStatusLabel(
  delivery: DeliveryStatus | undefined,
  sentAt?: unknown,
) {
  if (sentAt) {
    return `Sent ${formatInvitationDate(sentAt)}`;
  }
  if (!delivery) {
    return "Status unavailable";
  }
  switch (delivery.status) {
    case "queued":
      if (delivery.attempts > 0) {
        return delivery.next_retry_at
          ? `Retrying after ${formatInvitationDate(delivery.next_retry_at)}`
          : "Retrying";
      }
      return "Pending";
    case "failed":
      return delivery.failure
        ? `Failed (${deliveryFailureLabel(delivery.failure)})`
        : "Failed";
    case "cancelled":
      return "Cancelled";
    case "sent":
      return "Sent";
    default:
      return "Status unavailable";
  }
}

function automaticReminderLabel(invitation: Invitation) {
  if (invitation.automatic_reminded_at) {
    return deliveryStatusLabel(
      invitation.automatic_reminder_delivery,
      invitation.automatic_reminded_at,
    );
  }
  if (invitation.status !== "active") {
    return `Will not be sent because the Invitation is ${invitation.status}`;
  }
  if (
    invitation.automatic_reminder_delivery?.status === "queued" &&
    invitation.automatic_reminder_delivery.attempts === 0
  ) {
    return `Scheduled ${formatInvitationDate(invitation.automatic_reminder_scheduled_at)}`;
  }
  return deliveryStatusLabel(invitation.automatic_reminder_delivery);
}

function RecipientControls({
  person,
  session,
}: {
  person: Person;
  session: SessionResponse;
}) {
  const queryClient = useQueryClient();
  const isCurator = person.roles.includes("curator");
  const [email, setEmail] = useState("");
  const [recoveryEmail, setRecoveryEmail] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [recovery, setRecovery] = useState<RecoveryStartResponse>();
  const recipient = useQuery({
    queryKey: ["recipient", person.id],
    queryFn: () => apiJSON<Recipient>(`/api/recipients/${person.id}`),
    enabled: Boolean(person.current_recipient_access),
    retry: false,
  });

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["recipient", person.id] }),
      queryClient.invalidateQueries({
        queryKey: ["recipient-sessions", person.id],
      }),
      queryClient.invalidateQueries({ queryKey: ["people"] }),
    ]);
  }

  const designate = useMutation({
    mutationFn: (request: DesignateRequest) =>
      apiJSON<Recipient>(`/api/recipients/${person.id}/designate`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: refresh,
  });
  const lifecycleAction = useMutation({
    mutationFn: ({
      action,
      accessID,
    }: {
      action: "suspend" | "restore" | "revoke";
      accessID: string;
    }) =>
      apiJSON<Recipient>(`/api/recipients/${person.id}/${action}`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ access_id: accessID }),
      }),
    onSuccess: refresh,
  });
  const recipientSessions = useQuery({
    queryKey: ["recipient-sessions", person.id],
    queryFn: () =>
      apiJSON<SessionListResponse>(`/api/recipients/${person.id}/sessions`),
    enabled:
      Boolean(person.current_recipient_access) &&
      person.current_recipient_access?.state !== "pending",
    retry: false,
  });
  const startRecovery = useMutation({
    mutationFn: () =>
      apiJSON<RecoveryStartResponse>(
        `/api/recipients/${person.id}/email-recovery/request`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify({ new_email: recoveryEmail }),
        },
      ),
    onSuccess: setRecovery,
  });
  const completeRecovery = useMutation({
    mutationFn: () => {
      if (!recovery) throw new Error("Start recovery first.");
      return apiNoContent(
        `/api/recipients/${person.id}/email-recovery/complete`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify({
            recovery_id: recovery.recovery_id,
            code: recoveryCode,
          }),
        },
      );
    },
    onSuccess: async () => {
      setRecovery(undefined);
      setRecoveryCode("");
      await refresh();
    },
  });
  const invitationAction = useMutation({
    mutationFn: ({
      action,
      invitationID,
    }: {
      action: "send" | "revoke" | "reissue" | "remind";
      invitationID?: string;
    }) => {
      const request: InvitationActionRequest | undefined = invitationID
        ? { invitation_id: invitationID }
        : undefined;
      return apiJSON<Recipient>(
        `/api/recipients/${person.id}/invitation/${action}`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: request ? JSON.stringify(request) : undefined,
        },
      );
    },
    onSuccess: refresh,
  });

  const current = recipient.data;
  const invitation = current?.invitation;
  const error =
    designate.error ??
    invitationAction.error ??
    lifecycleAction.error ??
    startRecovery.error ??
    completeRecovery.error ??
    recipientSessions.error ??
    recipient.error;
  return (
    <section
      className="recipient-controls"
      aria-labelledby={`recipient-${person.id}`}
    >
      <h3 id={`recipient-${person.id}`}>Recipient access</h3>
      {!person.current_recipient_access ? (
        <>
          <p>
            Designation creates Pending Recipient access without sending email
            or granting Media access.
          </p>
          <label>
            Login email
            <input
              autoComplete="email"
              maxLength={320}
              onChange={(event) => setEmail(event.target.value)}
              type="email"
              value={email}
            />
          </label>
          <button
            disabled={designate.isPending || !email}
            onClick={() => designate.mutate({ email })}
            type="button"
          >
            Designate Pending Recipient
          </button>
        </>
      ) : null}
      {current ? (
        <>
          <p>
            Generation {current.access.generation}, {current.access.state}.
            Login email: {current.email}.
          </p>
          {!invitation && current.access.state === "pending" ? (
            <button
              disabled={invitationAction.isPending}
              onClick={() => invitationAction.mutate({ action: "send" })}
              type="button"
            >
              Create and send Invitation
            </button>
          ) : null}
          {isCurator ? (
            <p>
              Curator access cannot be suspended, revoked, or recovered here.
              Use signed-in email change for Curator identity changes.
            </p>
          ) : null}
          {!isCurator && current.access.state === "completed" ? (
            <button
              disabled={lifecycleAction.isPending}
              onClick={() =>
                lifecycleAction.mutate({
                  action: "suspend",
                  accessID: current.access.id,
                })
              }
              type="button"
            >
              Suspend Recipient access
            </button>
          ) : null}
          {!isCurator && current.access.state === "suspended" ? (
            <button
              disabled={lifecycleAction.isPending}
              onClick={() =>
                lifecycleAction.mutate({
                  action: "restore",
                  accessID: current.access.id,
                })
              }
              type="button"
            >
              Lift Suspension
            </button>
          ) : null}
          {!isCurator &&
          ["pending", "onboarding", "completed", "suspended"].includes(
            current.access.state,
          ) ? (
            <button
              className="danger-button"
              disabled={lifecycleAction.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    `Revoke access generation ${current.access.generation} for ${person.display_name}? Every Session will be invalidated and reinvitation will create an isolated generation.`,
                  )
                ) {
                  lifecycleAction.mutate({
                    action: "revoke",
                    accessID: current.access.id,
                  });
                }
              }}
              type="button"
            >
              Revoke Recipient access generation
            </button>
          ) : null}
          {recipientSessions.data?.sessions.length ? (
            <details>
              <summary>Inspect Recipient Sessions</summary>
              {recipientSessions.data.sessions.map((item) => (
                <p key={item.id}>
                  <strong>
                    {item.label || `${item.browser} on ${item.platform}`}
                  </strong>
                  {` · ${item.session_type} · ${item.status} · created ${formatInvitationDate(item.created_at)} · last active ${formatInvitationDate(item.last_activity_at)}`}
                  {item.location ? ` · ${item.location}` : ""}
                </p>
              ))}
            </details>
          ) : null}
          {!isCurator &&
          ["completed", "suspended"].includes(current.access.state) ? (
            <div className="recipient-recovery">
              <h4>Curator email recovery</h4>
              {!recovery ? (
                <>
                  <label>
                    Replacement login email
                    <input
                      onChange={(event) => setRecoveryEmail(event.target.value)}
                      type="email"
                      value={recoveryEmail}
                    />
                  </label>
                  <button
                    disabled={startRecovery.isPending || !recoveryEmail}
                    onClick={() => startRecovery.mutate()}
                    type="button"
                  >
                    Send recovery code
                  </button>
                </>
              ) : (
                <>
                  <p>
                    Completing recovery preserves this Person and generation but
                    revokes every Session.
                  </p>
                  <label>
                    Recovery code
                    <input
                      inputMode="numeric"
                      onChange={(event) => setRecoveryCode(event.target.value)}
                      value={recoveryCode}
                    />
                  </label>
                  <button
                    disabled={completeRecovery.isPending}
                    onClick={() => completeRecovery.mutate()}
                    type="button"
                  >
                    Complete email recovery
                  </button>
                </>
              )}
            </div>
          ) : null}
          {invitation ? (
            <div className="invitation-status">
              <p>
                <strong>Invitation:</strong> {invitation.status}. Issued{" "}
                {formatInvitationDate(invitation.issued_at)}; expires{" "}
                {formatInvitationDate(invitation.expires_at)}.
              </p>
              <p>
                Initial delivery:{" "}
                {deliveryStatusLabel(
                  invitation.initial_delivery,
                  invitation.sent_at,
                )}
                . Automatic reminder: {automaticReminderLabel(invitation)}.
              </p>
              {invitation.manual_reminder_count > 0 ? (
                <p>
                  Manual reminders requested: {invitation.manual_reminder_count}
                  . Latest delivery:{" "}
                  {deliveryStatusLabel(
                    invitation.last_manual_reminder_delivery,
                    invitation.last_manual_reminded_at,
                  )}
                  .
                </p>
              ) : null}
              {invitation.status === "active" ? (
                <div className="recipient-actions">
                  <button
                    disabled={invitationAction.isPending || !invitation.sent_at}
                    onClick={() =>
                      invitationAction.mutate({
                        action: "remind",
                        invitationID: invitation.id,
                      })
                    }
                    title={
                      invitation.sent_at
                        ? undefined
                        : "Wait for the initial Invitation delivery"
                    }
                    type="button"
                  >
                    Send manual reminder
                  </button>
                  <button
                    disabled={invitationAction.isPending}
                    onClick={() =>
                      invitationAction.mutate({
                        action: "reissue",
                        invitationID: invitation.id,
                      })
                    }
                    type="button"
                  >
                    Reissue with new link
                  </button>
                  <button
                    className="danger-button"
                    disabled={invitationAction.isPending}
                    onClick={() =>
                      invitationAction.mutate({
                        action: "revoke",
                        invitationID: invitation.id,
                      })
                    }
                    type="button"
                  >
                    Revoke Invitation
                  </button>
                </div>
              ) : null}
              {(invitation.status === "revoked" ||
                invitation.status === "expired" ||
                invitation.status === "superseded") &&
              current.access.state === "pending" ? (
                <button
                  disabled={invitationAction.isPending}
                  onClick={() =>
                    invitationAction.mutate({
                      action: "reissue",
                      invitationID: invitation.id,
                    })
                  }
                  type="button"
                >
                  Reissue Invitation
                </button>
              ) : null}
            </div>
          ) : null}
        </>
      ) : null}
      <ErrorNotice error={error} />
    </section>
  );
}

function MergeConfirmation({
  preview,
  transferGeneration,
  emailResolution,
  onTransferGeneration,
  onEmailResolution,
  onConfirm,
  error,
  pending,
}: {
  preview: MergePreview;
  transferGeneration: boolean;
  emailResolution: string;
  onTransferGeneration: (value: boolean) => void;
  onEmailResolution: (value: string) => void;
  onConfirm: () => void;
  error: Error | null;
  pending: boolean;
}) {
  const requirementsMet =
    preview.can_merge &&
    (!preview.requires_generation_transfer || transferGeneration) &&
    (!preview.requires_email_resolution || emailResolution !== "");
  return (
    <div className="merge-preview" aria-live="polite">
      <h3>Merge preview</h3>
      <p>
        <strong>Survivor:</strong> {personOptionLabel(preview.survivor)}
      </p>
      <p>
        <strong>Source retained in history:</strong>{" "}
        {personOptionLabel(preview.source)}
      </p>
      <ul>
        <li>
          {preview.affected_references.sessions_invalidated} unrevoked Session
          records will be revoked.
        </li>
        <li>
          {preview.affected_references.historical_audit_rows_preserved}{" "}
          historical attribution rows remain attached to their original Person.
        </li>
        <li>
          {preview.affected_references.family_relationships_moved} Family
          relationship references will move to the survivor.
        </li>
        <li>
          {preview.affected_references.family_relationships_archived} active
          Family relationships will be archived because they would become
          duplicate or self-connections.
        </li>
        <li>
          {preview.affected_references.visibility_memberships_moved} Visibility
          circle memberships will move to the survivor.
        </li>
        <li>
          {preview.affected_references.interest_entries_moved} current Interest
          choices will move or reconcile against the survivor.
        </li>
        <li>
          {preview.affected_references.interest_history_owners_retained}{" "}
          Interest history owner references will remain attributed to the source
          Person.
        </li>
        <li>
          {preview.affected_references.attendance_entries_moved} confirmed
          Attendance entries will move to the survivor.
        </li>
        <li>
          {preview.affected_references.audience_overrides_moved} manual Audience
          overrides will move to the survivor.
        </li>
        <li>
          {preview.affected_references.audience_reasons_moved} proposal reasons
          will reference the survivor.
        </li>
        <li>
          Source roles:{" "}
          {preview.affected_references.source_roles.join(", ") || "None"}.
        </li>
        <li>
          Survivor roles:{" "}
          {preview.affected_references.survivor_roles.join(", ") || "None"}.
        </li>
        <li>
          {preview.affected_references.recipient_role_will_transfer
            ? "The source Recipient role moves with its current access generation; no other roles are combined."
            : "Roles are not combined or transferred."}
        </li>
        <li>Audience authority is unchanged.</li>
        <li>
          {preview.affected_references.current_recipient_generation_id
            ? `Recipient generation ${preview.source.current_recipient_access?.generation} (${preview.source.current_recipient_access?.state}) will become generation ${preview.affected_references.resulting_recipient_generation} for the survivor if explicitly transferred.`
            : "No current Recipient generation moves."}
        </li>
      </ul>
      {preview.current_curator_session_kept ? (
        <p>The current Curator Session stays signed in.</p>
      ) : null}
      {preview.blockers.map((blocker) => (
        <p className="form-error" key={blocker}>
          {blocker}
        </p>
      ))}
      {preview.requires_generation_transfer ? (
        <label className="inline-choice">
          <input
            checked={transferGeneration}
            onChange={(event) => onTransferGeneration(event.target.checked)}
            type="checkbox"
          />
          Explicitly transfer current Recipient access generation
        </label>
      ) : null}
      {preview.requires_email_resolution ? (
        <label>
          Login email after transfer
          <select
            onChange={(event) => onEmailResolution(event.target.value)}
            value={emailResolution}
          >
            <option value="">Choose an email</option>
            <option value="keep_source">{preview.source_email}</option>
            <option value="keep_survivor">{preview.survivor_email}</option>
          </select>
        </label>
      ) : null}
      <ErrorNotice error={error} />
      <button
        className="danger-button"
        disabled={!requirementsMet || pending}
        onClick={onConfirm}
        type="button"
      >
        Confirm audited merge
      </button>
    </div>
  );
}
