import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import { apiJSON } from "./api";
import type {
  CreateRequest,
  ListResponse,
  MergePreview,
  MergeRequest,
  Person,
  UpdateRequest,
} from "./types/generated/people";
import type { SessionResponse } from "./types/generated/setup";

function ErrorNotice({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
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
    await queryClient.invalidateQueries({ queryKey: ["people"] });
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
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Name or sort name"
            type="search"
            value={search}
          />
        </label>
        <label className="inline-choice">
          <input
            checked={includeArchived}
            onChange={(event) => setIncludeArchived(event.target.checked)}
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
              onArchive={() => archivePerson.mutate(selected)}
              onUpdate={(request) =>
                updatePerson.mutate({ id: selected.id, request })
              }
              person={selected}
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
                      {person.display_name}
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
                      {person.display_name}
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
}: {
  person: Person;
  onUpdate: (request: UpdateRequest) => void;
  onArchive: () => void;
  updateError: Error | null;
  archiveError: Error | null;
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
          <dt>Active Sessions</dt>
          <dd>{person.active_sessions}</dd>
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
        <button className="danger-button" onClick={onArchive} type="button">
          Archive Person
        </button>
      ) : null}
    </form>
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
        <strong>Survivor:</strong> {preview.survivor.display_name}
      </p>
      <p>
        <strong>Source retained in history:</strong>{" "}
        {preview.source.display_name}
      </p>
      <ul>
        <li>
          {preview.affected_references.sessions_invalidated} Sessions will be
          invalidated.
        </li>
        <li>
          {preview.affected_references.historical_audit_rows_preserved}{" "}
          historical attribution rows remain attached to their original Person.
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
            ? "One current Recipient generation can be explicitly transferred."
            : "No current Recipient generation moves."}
        </li>
      </ul>
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
