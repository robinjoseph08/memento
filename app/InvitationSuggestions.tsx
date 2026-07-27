import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";

import { apiJSON } from "./api";
import type {
  ListResponse as PeopleListResponse,
  Person,
} from "./types/generated/people";
import type { SessionResponse } from "./types/generated/setup";
import type {
  AcceptRequest,
  CuratorListResponse,
  CuratorSuggestion,
  RequesterListResponse,
  RequesterSuggestion,
  SubmitRequest,
} from "./types/generated/suggestions";

function ErrorNotice({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

function formatSubmittedAt(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? "Unknown"
    : new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

function statusLabel(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

type PersonOption = Pick<
  Person,
  "id" | "display_name" | "sort_name" | "current_login_email"
>;

function personOptionLabel(person: PersonOption) {
  const details = [
    person.sort_name,
    person.current_login_email,
    person.id.slice(0, 8),
  ].filter(Boolean);
  return `${person.display_name} (${details.join(" · ")})`;
}

function RequesterSuggestions({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [relationshipContext, setRelationshipContext] = useState("");
  const [spokeWithPerson, setSpokeWithPerson] = useState<"" | "yes" | "no">("");
  const suggestions = useQuery({
    queryKey: ["invitation-suggestions", "requester"],
    queryFn: () =>
      apiJSON<RequesterListResponse>("/api/invitation-suggestions"),
  });
  const submit = useMutation({
    mutationFn: (request: SubmitRequest) =>
      apiJSON<RequesterSuggestion>("/api/invitation-suggestions", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify(request),
      }),
    onSuccess: async () => {
      setName("");
      setEmail("");
      setRelationshipContext("");
      setSpokeWithPerson("");
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["invitation-suggestions", "requester"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["invitation-suggestions", "curator"],
        }),
      ]);
    },
  });
  const withdraw = useMutation({
    mutationFn: (id: string) =>
      apiJSON<RequesterSuggestion>(
        `/api/invitation-suggestions/${id}/withdraw`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
        },
      ),
    onSettled: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["invitation-suggestions", "requester"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["invitation-suggestions", "curator"],
        }),
      ]);
      withdraw.reset();
    },
  });

  function submitSuggestion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (spokeWithPerson === "") return;
    submit.mutate({
      name,
      email,
      relationship_context: relationshipContext,
      spoke_with_person: spokeWithPerson === "yes",
    });
  }

  return (
    <section
      aria-labelledby="request-invitation-title"
      className="suggestion-panel"
    >
      <header>
        <p className="eyebrow">RECIPIENT SUGGESTIONS</p>
        <h2 id="request-invitation-title">Suggest someone to the Curator</h2>
        <p>
          This submits a suggestion only. The Curator separately decides whether
          to create or match a Person, designate Recipient access, and send an
          Invitation.
        </p>
      </header>
      <form className="suggestion-form" onSubmit={submitSuggestion}>
        <label>
          Person's name
          <input
            autoComplete="name"
            maxLength={120}
            onChange={(event) => setName(event.target.value)}
            required
            value={name}
          />
        </label>
        <label>
          Email
          <input
            autoComplete="email"
            maxLength={320}
            onChange={(event) => setEmail(event.target.value)}
            required
            type="email"
            value={email}
          />
        </label>
        <label>
          Relationship context
          <textarea
            maxLength={1000}
            onChange={(event) => setRelationshipContext(event.target.value)}
            required
            value={relationshipContext}
          />
        </label>
        <fieldset>
          <legend>Have you already spoken with this Person?</legend>
          <label className="radio-choice">
            <input
              checked={spokeWithPerson === "yes"}
              name="spoke-with-person"
              onChange={() => setSpokeWithPerson("yes")}
              required
              type="radio"
            />
            Yes
          </label>
          <label className="radio-choice">
            <input
              checked={spokeWithPerson === "no"}
              name="spoke-with-person"
              onChange={() => setSpokeWithPerson("no")}
              required
              type="radio"
            />
            No
          </label>
        </fieldset>
        <ErrorNotice error={submit.error} />
        <button disabled={submit.isPending} type="submit">
          {submit.isPending ? "Submitting…" : "Submit suggestion"}
        </button>
      </form>

      <div className="suggestion-history">
        <h3>Your suggestions</h3>
        {suggestions.isPending ? <p>Loading suggestions…</p> : null}
        <ErrorNotice error={suggestions.error ?? withdraw.error} />
        {suggestions.data?.suggestions.length === 0 ? (
          <p>No Invitation suggestions yet.</p>
        ) : null}
        {suggestions.data?.suggestions.map((suggestion) => (
          <article className="suggestion-row" key={suggestion.id}>
            <div>
              <strong>{suggestion.name}</strong>
              <p>
                {suggestion.email} · Submitted{" "}
                {formatSubmittedAt(suggestion.submitted_at)}
              </p>
            </div>
            <div className="suggestion-status">
              <span>{statusLabel(suggestion.status)}</span>
              {suggestion.status === "submitted" ? (
                <button
                  disabled={withdraw.isPending}
                  onClick={() => withdraw.mutate(suggestion.id)}
                  type="button"
                >
                  Withdraw
                </button>
              ) : null}
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function CuratorSuggestionCard({
  suggestion,
  session,
}: {
  suggestion: CuratorSuggestion;
  session: SessionResponse;
}) {
  const queryClient = useQueryClient();
  const [personID, setPersonID] = useState("");
  const [personSearch, setPersonSearch] = useState("");
  const [newName, setNewName] = useState(suggestion.name);
  const [newSortName, setNewSortName] = useState("");
  const people = useQuery({
    queryKey: ["people", "suggestion-matches", personSearch],
    queryFn: () =>
      apiJSON<PeopleListResponse>(
        `/api/people?query=${encodeURIComponent(personSearch)}&include_archived=false`,
      ),
    enabled: personSearch.trim() !== "",
  });
  const personOptions = new Map<string, PersonOption>();
  for (const match of suggestion.matching_people) {
    personOptions.set(match.person_id, {
      id: match.person_id,
      display_name: match.display_name,
      sort_name: match.sort_name,
      current_login_email: match.current_login_email,
    });
  }
  for (const person of people.data?.people ?? []) {
    if (person.status === "current") personOptions.set(person.id, person);
  }
  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: ["invitation-suggestions"],
      }),
      queryClient.invalidateQueries({ queryKey: ["people"] }),
    ]);
  };
  const accept = useMutation({
    mutationFn: (request: AcceptRequest) =>
      apiJSON<CuratorSuggestion>(
        `/api/invitation-suggestions/curator/${suggestion.id}/accept`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify(request),
        },
      ),
    onSettled: refresh,
  });
  const reject = useMutation({
    mutationFn: () =>
      apiJSON<CuratorSuggestion>(
        `/api/invitation-suggestions/curator/${suggestion.id}/reject`,
        {
          method: "POST",
          headers: { "X-Memento-CSRF": session.csrf_token },
        },
      ),
    onSettled: refresh,
  });
  const pending = accept.isPending || reject.isPending;
  return (
    <article className="suggestion-review-card">
      <header>
        <div>
          <h3>{suggestion.name}</h3>
          <p>
            Suggested by {suggestion.requester_name} · {suggestion.email}
          </p>
        </div>
        <span>{statusLabel(suggestion.status)}</span>
      </header>
      <p>{suggestion.relationship_context}</p>
      <p>
        Already spoke with this Person:{" "}
        {suggestion.spoke_with_person ? "Yes" : "No"}
      </p>
      {suggestion.duplicate_suggestion_count > 0 ? (
        <p className="suggestion-match-note">
          {suggestion.duplicate_suggestion_count} other suggestion
          {suggestion.duplicate_suggestion_count === 1 ? " uses" : "s use"} this
          email. No identity was matched automatically.
        </p>
      ) : null}
      {suggestion.status === "submitted" ? (
        <div className="suggestion-resolution">
          {suggestion.matching_people.length > 0 ? (
            <p className="suggestion-match-note">
              Possible existing match:{" "}
              {suggestion.matching_people
                .map((match) => match.display_name)
                .join(", ")}
            </p>
          ) : null}
          <label>
            Search current People
            <input
              onChange={(event) => setPersonSearch(event.target.value)}
              placeholder="Search by display or sort name"
              value={personSearch}
            />
          </label>
          <ErrorNotice error={people.error} />
          <label>
            Match an existing Person
            <select
              onChange={(event) => setPersonID(event.target.value)}
              value={personID}
            >
              <option value="">Choose a current Person</option>
              {[...personOptions.values()].map((person) => (
                <option key={person.id} value={person.id}>
                  {personOptionLabel(person)}
                </option>
              ))}
            </select>
          </label>
          <button
            disabled={pending || !personID}
            onClick={() => accept.mutate({ person_id: personID })}
            type="button"
          >
            Match Person and accept
          </button>
          <div className="suggestion-create-person">
            <label>
              New Person name
              <input
                maxLength={120}
                onChange={(event) => setNewName(event.target.value)}
                value={newName}
              />
            </label>
            <label>
              New Person sort name
              <input
                maxLength={120}
                onChange={(event) => setNewSortName(event.target.value)}
                placeholder="Defaults to display name"
                value={newSortName}
              />
            </label>
            <button
              disabled={pending || !newName.trim()}
              onClick={() =>
                accept.mutate({
                  person_id: "",
                  create_person: {
                    display_name: newName,
                    sort_name: newSortName,
                  },
                })
              }
              type="button"
            >
              Create Person and accept
            </button>
          </div>
          <button
            className="danger-button"
            disabled={pending}
            onClick={() => reject.mutate()}
            type="button"
          >
            Reject suggestion
          </button>
          <ErrorNotice error={accept.error ?? reject.error} />
          <p className="suggestion-separate-action">
            Acceptance does not designate Recipient access or send an
            Invitation. Use People management for either separate decision.
          </p>
        </div>
      ) : null}
      {suggestion.status === "accepted" ? (
        <p>
          Matched to {suggestion.matched_person_name}. Recipient access and
          Invitation state remain separate.
        </p>
      ) : null}
    </article>
  );
}

function CuratorSuggestions({ session }: { session: SessionResponse }) {
  const suggestions = useQuery({
    queryKey: ["invitation-suggestions", "curator"],
    queryFn: () =>
      apiJSON<CuratorListResponse>("/api/invitation-suggestions/curator"),
  });
  return (
    <section
      aria-labelledby="curator-suggestions-title"
      className="suggestion-panel"
    >
      <header>
        <p className="eyebrow">CURATOR REVIEW</p>
        <h2 id="curator-suggestions-title">Invitation suggestions</h2>
        <p>
          Reject, match, or create a Person explicitly. Recipient designation
          and Invitation sending remain separate actions.
        </p>
      </header>
      <ErrorNotice error={suggestions.error} />
      {suggestions.isPending ? <p>Loading review queue…</p> : null}
      {suggestions.data?.suggestions.length === 0 ? (
        <p>No suggestions to review.</p>
      ) : null}
      <div className="suggestion-review-list">
        {suggestions.data?.suggestions.map((suggestion) => (
          <CuratorSuggestionCard
            key={suggestion.id}
            session={session}
            suggestion={suggestion}
          />
        ))}
      </div>
    </section>
  );
}

export function InvitationSuggestions({
  session,
}: {
  session: SessionResponse;
}) {
  return (
    <div className="suggestions-shell">
      <RequesterSuggestions session={session} />
      {session.curator ? <CuratorSuggestions session={session} /> : null}
    </div>
  );
}
