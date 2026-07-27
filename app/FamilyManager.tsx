import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import { apiJSON } from "./api";
import type {
  BranchResponse,
  ListResponse as FamilyListResponse,
  MutationRequest,
  Relationship,
} from "./types/generated/family";
import type {
  ListResponse as PeopleListResponse,
  Person,
} from "./types/generated/people";
import type { SessionResponse } from "./types/generated/setup";

function ErrorNotice({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

function relationshipLabel(relationship: Relationship) {
  switch (relationship.relationship_type) {
    case "parent_child":
      return `${relationship.person_a.display_name} is parent of ${relationship.person_b.display_name}`;
    case "sibling":
      return `${relationship.person_a.display_name} and ${relationship.person_b.display_name} are siblings`;
    default:
      return `${relationship.person_a.display_name} and ${relationship.person_b.display_name} are ${relationship.partner_status} partners`;
  }
}

function connectionLabel(connection: string, generation: number) {
  switch (connection) {
    case "current_partner":
      return "Current partner";
    case "descendant_current_partner":
      return `Current partner of a generation ${generation} descendant`;
    default:
      return `Generation ${generation} descendant`;
  }
}

type PersonOption = Pick<
  Person,
  "id" | "display_name" | "sort_name" | "status"
>;

function personOption(person: PersonOption) {
  return `${person.display_name} (${person.sort_name})`;
}

function PersonPicker({
  label,
  value,
  onChange,
  disabled = false,
  required = false,
  selectedPerson,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  required?: boolean;
  selectedPerson?: PersonOption;
}) {
  const [search, setSearch] = useState("");
  const [rememberedPerson, setRememberedPerson] = useState<PersonOption>();
  const people = useQuery({
    queryKey: ["family-people", search],
    queryFn: () =>
      apiJSON<PeopleListResponse>(
        `/api/people?query=${encodeURIComponent(search)}&include_archived=false`,
      ),
  });
  const options: PersonOption[] = [...(people.data?.people ?? [])];
  for (const person of [selectedPerson, rememberedPerson]) {
    if (
      person?.status === "current" &&
      !options.some((option) => option.id === person.id)
    ) {
      options.push(person);
    }
  }

  return (
    <div className="family-person-picker">
      <label>
        Search {label}
        <input
          disabled={disabled}
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Name or sort name"
          type="search"
          value={search}
        />
      </label>
      <label>
        {label}
        <select
          disabled={disabled}
          onChange={(event) => {
            const nextValue = event.target.value;
            setRememberedPerson(
              options.find((person) => person.id === nextValue),
            );
            onChange(nextValue);
          }}
          required={required}
          value={value}
        >
          <option value="">Choose a Person</option>
          {options.map((person) => (
            <option key={person.id} value={person.id}>
              {personOption(person)}
            </option>
          ))}
        </select>
      </label>
      <ErrorNotice error={people.error} />
    </div>
  );
}

export function FamilyManager({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [includeArchived, setIncludeArchived] = useState(false);
  const [selectedRelationshipID, setSelectedRelationshipID] = useState("");
  const [relationshipType, setRelationshipType] = useState("parent_child");
  const [personAID, setPersonAID] = useState("");
  const [personBID, setPersonBID] = useState("");
  const [partnerStatus, setPartnerStatus] = useState("current");
  const [branchPersonID, setBranchPersonID] = useState("");

  const relationships = useQuery({
    queryKey: ["family-relationships", includeArchived],
    queryFn: () =>
      apiJSON<FamilyListResponse>(
        `/api/relationships?include_archived=${includeArchived}`,
      ),
  });
  const branch = useQuery({
    queryKey: ["family-branch", branchPersonID],
    queryFn: () =>
      apiJSON<BranchResponse>(`/api/relationships/branches/${branchPersonID}`),
    enabled: branchPersonID !== "",
  });

  const selected = relationships.data?.relationships.find(
    (relationship) => relationship.id === selectedRelationshipID,
  );

  async function refreshFamily() {
    setSelectedRelationshipID("");
    setRelationshipType("parent_child");
    setPersonAID("");
    setPersonBID("");
    setPartnerStatus("current");
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["family-relationships"] }),
      queryClient.invalidateQueries({ queryKey: ["family-branch"] }),
    ]);
  }

  const saveRelationship = useMutation({
    mutationFn: (request: MutationRequest) =>
      selected
        ? apiJSON<Relationship>(`/api/relationships/${selected.id}`, {
            method: "PATCH",
            headers: { "X-Memento-CSRF": session.csrf_token },
            body: JSON.stringify(request),
          })
        : apiJSON<Relationship>("/api/relationships", {
            method: "POST",
            headers: { "X-Memento-CSRF": session.csrf_token },
            body: JSON.stringify(request),
          }),
    onSuccess: refreshFamily,
  });
  const archiveRelationship = useMutation({
    mutationFn: (relationship: Relationship) =>
      apiJSON<Relationship>(`/api/relationships/${relationship.id}/archive`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({ version: relationship.version }),
      }),
    onSuccess: refreshFamily,
  });

  function clearForm() {
    setSelectedRelationshipID("");
    setRelationshipType("parent_child");
    setPersonAID("");
    setPersonBID("");
    setPartnerStatus("current");
    saveRelationship.reset();
    archiveRelationship.reset();
  }

  function inspectRelationship(relationship: Relationship) {
    setSelectedRelationshipID(relationship.id);
    setRelationshipType(relationship.relationship_type);
    setPersonAID(relationship.person_a.id);
    setPersonBID(relationship.person_b.id);
    setPartnerStatus(relationship.partner_status || "current");
    saveRelationship.reset();
    archiveRelationship.reset();
  }

  function submitRelationship(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveRelationship.mutate({
      relationship_type: relationshipType,
      person_a_id: personAID,
      person_b_id: personBID,
      partner_status: relationshipType === "partner" ? partnerStatus : "",
      ...(selected ? { version: selected.version } : {}),
    });
  }

  return (
    <section aria-labelledby="family-title" className="family-shell">
      <header className="family-header">
        <div>
          <p className="eyebrow">CONNECTIONS, NOT ACCESS</p>
          <h2 id="family-title">Family relationships</h2>
          <p>
            Record explicit connections between People. A Family relationship
            helps describe choices, but never grants Recipient access.
          </p>
        </div>
      </header>

      <div className="family-explainer">
        <p>
          <strong>Parent-child</strong> is directional and must stay acyclic.
          Descendants appear in a Family branch.
        </p>
        <p>
          <strong>Current partners</strong> appear in Family branches. Former
          partners and explicitly recorded siblings do not, unless another
          qualifying connection reaches them.
        </p>
      </div>

      <div className="family-layout">
        <section aria-labelledby="connections-title" className="people-panel">
          <div className="family-panel-heading">
            <h3 id="connections-title">Recorded connections</h3>
            <label className="inline-choice">
              <input
                checked={includeArchived}
                onChange={(event) => {
                  setIncludeArchived(event.target.checked);
                  clearForm();
                }}
                type="checkbox"
              />
              Include archived
            </label>
          </div>
          <ErrorNotice error={relationships.error} />
          <div className="relationship-list">
            {relationships.data?.relationships.map((relationship) => (
              <button
                className={
                  relationship.id === selectedRelationshipID
                    ? "relationship-row selected"
                    : "relationship-row"
                }
                key={relationship.id}
                onClick={() => inspectRelationship(relationship)}
                type="button"
              >
                <strong>{relationshipLabel(relationship)}</strong>
                <span>
                  {relationship.archived_at
                    ? "Archived"
                    : `Active · version ${relationship.version}`}
                </span>
              </button>
            ))}
            {relationships.isSuccess &&
            relationships.data.relationships.length === 0 ? (
              <p>
                {includeArchived
                  ? "No Family relationships recorded."
                  : "No active Family relationships."}
              </p>
            ) : null}
          </div>
        </section>

        <form className="people-panel" onSubmit={submitRelationship}>
          <div className="family-panel-heading">
            <h3>
              {selected?.archived_at
                ? "Archived connection"
                : selected
                  ? "Edit connection"
                  : "Create connection"}
            </h3>
            {selected ? (
              <button onClick={clearForm} type="button">
                Create another
              </button>
            ) : null}
          </div>
          <label>
            Connection type
            <select
              disabled={Boolean(selected?.archived_at)}
              onChange={(event) => setRelationshipType(event.target.value)}
              value={relationshipType}
            >
              <option value="parent_child">Parent-child</option>
              <option value="sibling">Sibling</option>
              <option value="partner">Partner</option>
            </select>
          </label>
          <PersonPicker
            disabled={Boolean(selected?.archived_at)}
            label={
              relationshipType === "parent_child" ? "Parent" : "First Person"
            }
            onChange={setPersonAID}
            required
            selectedPerson={selected?.person_a}
            value={personAID}
          />
          <PersonPicker
            disabled={Boolean(selected?.archived_at)}
            label={
              relationshipType === "parent_child" ? "Child" : "Second Person"
            }
            onChange={setPersonBID}
            required
            selectedPerson={selected?.person_b}
            value={personBID}
          />
          {relationshipType === "partner" ? (
            <label>
              Partner connection
              <select
                disabled={Boolean(selected?.archived_at)}
                onChange={(event) => setPartnerStatus(event.target.value)}
                value={partnerStatus}
              >
                <option value="current">Current partners</option>
                <option value="former">Former partners</option>
              </select>
            </label>
          ) : null}
          <ErrorNotice
            error={saveRelationship.error ?? archiveRelationship.error}
          />
          {!selected?.archived_at ? (
            <button disabled={saveRelationship.isPending} type="submit">
              {selected ? "Save connection" : "Create connection"}
            </button>
          ) : null}
          {selected && !selected.archived_at ? (
            <button
              className="danger-button"
              disabled={archiveRelationship.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    `Archive this connection: ${relationshipLabel(selected)}?`,
                  )
                ) {
                  archiveRelationship.mutate(selected);
                }
              }}
              type="button"
            >
              Archive connection
            </button>
          ) : null}
        </form>

        <section
          aria-labelledby="branch-title"
          className="people-panel branch-panel"
        >
          <h3 id="branch-title">Inspect Family branch</h3>
          <p>
            A branch contains current partners, descendants, and descendants'
            current partners through every generation. It does not add anyone to
            an Interest list and never grants Recipient access.
          </p>
          <PersonPicker
            label="Branch root"
            onChange={setBranchPersonID}
            selectedPerson={branch.data?.root}
            value={branchPersonID}
          />
          <ErrorNotice error={branch.error} />
          {branch.isSuccess ? (
            <div className="branch-results" aria-live="polite">
              <h4>{branch.data.root.display_name}'s Family branch</h4>
              {branch.data.members.length ? (
                <ul>
                  {branch.data.members.map((member) => (
                    <li key={member.person.id}>
                      <strong>{member.person.display_name}</strong>
                      <span>
                        {connectionLabel(
                          member.connection_type,
                          member.generation,
                        )}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>No People are in this Family branch.</p>
              )}
            </div>
          ) : null}
        </section>
      </div>
    </section>
  );
}
