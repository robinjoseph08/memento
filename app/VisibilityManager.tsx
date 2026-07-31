import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, type FormEvent } from "react";

import { apiJSON, apiNoContent } from "./api";
import type {
  ListResponse as PeopleListResponse,
  Person as CuratorPerson,
} from "./types/generated/people";
import type { SessionResponse } from "./types/generated/setup";
import type {
  Circle,
  CircleListResponse,
  CircleRequest,
  CircleVersionRequest,
  DiscoveryResponse,
  InterestListResponse,
  InterestMutationRequest,
  MembershipRequest,
  Person,
} from "./types/generated/visibility";

function ErrorNotice({ error }: { error: Error | null }) {
  return error ? (
    <p className="form-error" role="alert">
      {error.message}
    </p>
  ) : null;
}

function byName(left: Person, right: Person) {
  return (
    left.sort_name.localeCompare(right.sort_name, undefined, {
      sensitivity: "base",
    }) || left.id.localeCompare(right.id)
  );
}

function formatHistoryAction(action: string, result: string) {
  if (action === "deactivated") return "deactivated after visibility loss";
  if (action === "moved") return `moved during a Person merge as ${result}`;
  if (result === "deselected") return "removed";
  return "selected";
}

function formatRelationship(person: Person) {
  const annotation = person.relationship;
  if (!annotation) return "";
  const label = annotation.connection_type.replaceAll("_", " ");
  return annotation.generation
    ? `${label}, generation ${annotation.generation}`
    : label;
}

async function discoverAll(path: string): Promise<DiscoveryResponse> {
  const people: Person[] = [];
  let cursor: string | undefined;
  do {
    const query = new URLSearchParams({ limit: "200" });
    if (cursor) query.set("cursor", cursor);
    const page = await apiJSON<DiscoveryResponse>(`${path}?${query}`);
    people.push(...page.people);
    cursor = page.next_cursor;
  } while (cursor);
  return { people };
}

function historyPageURL(path: string, cursor: string) {
  const query = new URLSearchParams({
    history_cursor: cursor,
    history_limit: "50",
  });
  return `${path}?${query}`;
}

function InterestChoice({
  person,
  entry,
  currentlyDiscoverable,
  pending,
  onChange,
}: {
  person: Person;
  entry?: InterestListResponse["entries"][number];
  currentlyDiscoverable: boolean;
  pending: boolean;
  onChange: (selected: boolean) => void;
}) {
  const active = entry?.state === "active";
  const inactive = entry?.state === "ineligible";
  const status = inactive
    ? currentlyDiscoverable
      ? "Inactive after visibility loss. Select again to restore."
      : "Inactive because this Person is no longer discoverable."
    : active
      ? "Selected explicitly"
      : "Not selected";
  const content = (
    <>
      <input
        aria-label={person.display_name}
        checked={active}
        disabled={pending || (inactive && !currentlyDiscoverable)}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
      <span>
        <strong>{person.display_name}</strong>
        {person.relationship ? (
          <small>{formatRelationship(person)}</small>
        ) : null}
        <small className="interest-choice-status">{status}</small>
      </span>
    </>
  );
  if (inactive) {
    return (
      <div className="choice">
        {content}
        <button
          disabled={pending}
          onClick={() => onChange(false)}
          type="button"
        >
          Remove retained choice
        </button>
      </div>
    );
  }
  return <label className="choice">{content}</label>;
}

function InterestHistory({
  interest,
  pending,
  onLoadMore,
}: {
  interest: InterestListResponse;
  pending: boolean;
  onLoadMore: (cursor: string) => void;
}) {
  return (
    <details>
      <summary>Interest list audit history</summary>
      {interest.history.length ? (
        <ol className="interest-history">
          {interest.history.map((history) => (
            <li key={history.id}>
              <strong>{history.person.display_name}</strong>{" "}
              {formatHistoryAction(history.action, history.result)} by{" "}
              {history.actor.display_name}{" "}
              <time dateTime={history.created_at}>
                {new Date(history.created_at).toLocaleString()}
              </time>
            </li>
          ))}
        </ol>
      ) : (
        <p>No Interest list changes yet.</p>
      )}
      {interest.history_next_cursor ? (
        <button
          disabled={pending}
          onClick={() => onLoadMore(interest.history_next_cursor!)}
          type="button"
        >
          {pending ? "Loading history…" : "Load older history"}
        </button>
      ) : null}
    </details>
  );
}

export function RecipientVisibilityManager({
  session,
  onSignOut,
}: {
  session: SessionResponse;
  onSignOut: () => void;
}) {
  const queryClient = useQueryClient();
  const interest = useQuery({
    queryKey: ["recipient-interest-list"],
    queryFn: () => apiJSON<InterestListResponse>("/api/me/interest-list"),
  });
  const discoverable = useQuery({
    queryKey: ["recipient-discoverable"],
    queryFn: () => discoverAll("/api/me/people"),
  });
  const choices = useMemo(() => {
    const people = new Map<string, Person>();
    for (const person of discoverable.data?.people ?? []) {
      people.set(person.id, person);
    }
    for (const entry of interest.data?.entries ?? []) {
      if (!people.has(entry.person.id)) {
        people.set(entry.person.id, entry.person);
      }
    }
    return [...people.values()].sort(byName);
  }, [discoverable.data?.people, interest.data?.entries]);
  const mutateInterest = useMutation({
    mutationFn: ({ person, selected }: { person: Person; selected: boolean }) =>
      apiJSON<InterestListResponse>(`/api/me/interest-list/${person.id}`, {
        method: "PUT",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({
          selected,
          version: interest.data?.version ?? 0,
        } satisfies InterestMutationRequest),
      }),
    onSuccess: (response) => {
      queryClient.setQueryData(["recipient-interest-list"], response);
    },
  });
  const loadHistory = useMutation({
    mutationFn: (cursor: string) =>
      apiJSON<InterestListResponse>(
        historyPageURL("/api/me/interest-list", cursor),
      ),
    onSuccess: (page) => {
      queryClient.setQueryData<InterestListResponse>(
        ["recipient-interest-list"],
        (current) =>
          current
            ? {
                ...current,
                history: [...current.history, ...page.history],
                history_next_cursor: page.history_next_cursor,
              }
            : page,
      );
    },
  });
  const signOut = useMutation({
    mutationFn: () =>
      apiNoContent("/api/session/logout", {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
      }),
    onSuccess: onSignOut,
  });

  return (
    <section
      aria-labelledby="recipient-interest-title"
      className="visibility-shell"
    >
      <header className="visibility-header">
        <div>
          <p className="eyebrow">PRIVATE DISCOVERY</p>
          <h2 id="recipient-interest-title">Your Interest list</h2>
          <p>
            Choose discoverable People to help the Curator suggest relevant
            Event photos for you, including Events you did not attend. Every
            proposal is reviewed, and choices never grant access.
          </p>
        </div>
        <div className="header-actions">
          <span>Signed in as {session.display_name}</span>
          <button
            disabled={signOut.isPending}
            onClick={() => signOut.mutate()}
            type="button"
          >
            {signOut.isPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </header>
      <ErrorNotice
        error={
          interest.error ??
          discoverable.error ??
          mutateInterest.error ??
          loadHistory.error ??
          signOut.error
        }
      />
      {interest.isPending || discoverable.isPending ? (
        <p>Loading your private Interest list…</p>
      ) : null}
      {interest.isSuccess && discoverable.isSuccess ? (
        <div className="interest-editor">
          {choices.length ? (
            <div className="interest-choices">
              {choices.map((person) => {
                const entry = interest.data.entries.find(
                  (candidate) => candidate.person.id === person.id,
                );
                return (
                  <InterestChoice
                    currentlyDiscoverable={discoverable.data.people.some(
                      (candidate) => candidate.id === person.id,
                    )}
                    entry={entry}
                    key={person.id}
                    onChange={(selected) =>
                      mutateInterest.mutate({ person, selected })
                    }
                    pending={mutateInterest.isPending}
                    person={person}
                  />
                );
              })}
            </div>
          ) : (
            <p>No People are discoverable through shared circles.</p>
          )}
          <InterestHistory
            interest={interest.data}
            onLoadMore={(cursor) => loadHistory.mutate(cursor)}
            pending={loadHistory.isPending}
          />
        </div>
      ) : null}
    </section>
  );
}

export function VisibilityManager({ session }: { session: SessionResponse }) {
  const queryClient = useQueryClient();
  const [circleName, setCircleName] = useState("");
  const [editingCircleID, setEditingCircleID] = useState("");
  const [mobileCircleID, setMobileCircleID] = useState("");
  const [memberFilter, setMemberFilter] = useState("");
  const [recipientID, setRecipientID] = useState("");

  const people = useQuery({
    queryKey: ["visibility-people"],
    queryFn: () =>
      apiJSON<PeopleListResponse>("/api/people?query=&include_archived=false"),
  });
  const circles = useQuery({
    queryKey: ["visibility-circles"],
    queryFn: () =>
      apiJSON<CircleListResponse>(
        "/api/visibility-circles?include_archived=false",
      ),
  });
  const interest = useQuery({
    queryKey: ["curator-interest-list", recipientID],
    queryFn: () =>
      apiJSON<InterestListResponse>(`/api/interest-lists/${recipientID}`),
    enabled: recipientID !== "",
  });
  const discoverable = useQuery({
    queryKey: ["curator-discoverable", recipientID],
    queryFn: () =>
      discoverAll(`/api/interest-lists/${recipientID}/discoverable`),
    enabled: recipientID !== "",
  });

  const currentPeople = people.data?.people ?? [];
  const activeCircles = circles.data?.circles ?? [];
  const editingCircle = activeCircles.find(
    (circle) => circle.id === editingCircleID,
  );
  const recipients = currentPeople.filter(
    (person) =>
      person.roles.includes("recipient") && !person.roles.includes("curator"),
  );
  const mobileCircle =
    activeCircles.find((circle) => circle.id === mobileCircleID) ??
    activeCircles[0];
  const filteredPeople = currentPeople.filter((person) =>
    `${person.display_name} ${person.sort_name}`
      .toLocaleLowerCase()
      .includes(memberFilter.trim().toLocaleLowerCase()),
  );

  const interestChoices = useMemo(() => {
    const choices = new Map<string, Person>();
    for (const person of discoverable.data?.people ?? []) {
      choices.set(person.id, person);
    }
    for (const entry of interest.data?.entries ?? []) {
      if (!choices.has(entry.person.id)) {
        choices.set(entry.person.id, entry.person);
      }
    }
    return [...choices.values()].sort(byName);
  }, [discoverable.data?.people, interest.data?.entries]);

  const saveCircle = useMutation({
    mutationFn: () =>
      editingCircle
        ? apiJSON<Circle>(`/api/visibility-circles/${editingCircle.id}`, {
            method: "PATCH",
            headers: { "X-Memento-CSRF": session.csrf_token },
            body: JSON.stringify({
              name: circleName,
              version: editingCircle.version,
            } satisfies CircleRequest),
          })
        : apiJSON<Circle>("/api/visibility-circles", {
            method: "POST",
            headers: { "X-Memento-CSRF": session.csrf_token },
            body: JSON.stringify({ name: circleName } satisfies CircleRequest),
          }),
    onSuccess: async (circle) => {
      setCircleName("");
      setEditingCircleID("");
      setMobileCircleID(circle.id);
      await queryClient.invalidateQueries({
        queryKey: ["visibility-circles"],
      });
    },
  });
  const archiveCircle = useMutation({
    mutationFn: (circle: Circle) =>
      apiJSON<Circle>(`/api/visibility-circles/${circle.id}/archive`, {
        method: "POST",
        headers: { "X-Memento-CSRF": session.csrf_token },
        body: JSON.stringify({
          version: circle.version,
        } satisfies CircleVersionRequest),
      }),
    onSuccess: async () => {
      setCircleName("");
      setEditingCircleID("");
      setMobileCircleID("");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["visibility-circles"] }),
        queryClient.invalidateQueries({
          queryKey: ["curator-interest-list"],
        }),
        queryClient.invalidateQueries({ queryKey: ["curator-discoverable"] }),
      ]);
    },
  });
  const membership = useMutation({
    mutationFn: ({
      circle,
      person,
      included,
    }: {
      circle: Circle;
      person: CuratorPerson;
      included: boolean;
    }) =>
      apiJSON<Circle>(
        `/api/visibility-circles/${circle.id}/members/${person.id}`,
        {
          method: "PUT",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify({
            included,
            version: circle.version,
          } satisfies MembershipRequest),
        },
      ),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["visibility-circles"] }),
        queryClient.invalidateQueries({
          queryKey: ["curator-interest-list"],
        }),
        queryClient.invalidateQueries({ queryKey: ["curator-discoverable"] }),
      ]);
    },
  });
  const mutateInterest = useMutation({
    mutationFn: ({
      recipientID: mutationRecipientID,
      person,
      selected,
    }: {
      recipientID: string;
      person: Person;
      selected: boolean;
    }) =>
      apiJSON<InterestListResponse>(
        `/api/interest-lists/${mutationRecipientID}/people/${person.id}`,
        {
          method: "PUT",
          headers: { "X-Memento-CSRF": session.csrf_token },
          body: JSON.stringify({
            selected,
            version: interest.data?.version ?? 0,
          } satisfies InterestMutationRequest),
        },
      ),
    onSuccess: (response, variables) => {
      queryClient.setQueryData(
        ["curator-interest-list", variables.recipientID],
        response,
      );
    },
  });
  const loadHistory = useMutation({
    mutationFn: ({
      recipientID: historyRecipientID,
      cursor,
    }: {
      recipientID: string;
      cursor: string;
    }) =>
      apiJSON<InterestListResponse>(
        historyPageURL(`/api/interest-lists/${historyRecipientID}`, cursor),
      ),
    onSuccess: (page, variables) => {
      queryClient.setQueryData<InterestListResponse>(
        ["curator-interest-list", variables.recipientID],
        (current) =>
          current
            ? {
                ...current,
                history: [...current.history, ...page.history],
                history_next_cursor: page.history_next_cursor,
              }
            : page,
      );
    },
  });

  function submitCircle(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveCircle.mutate();
  }

  function startEditing(circle: Circle) {
    setEditingCircleID(circle.id);
    setCircleName(circle.name);
    saveCircle.reset();
    archiveCircle.reset();
  }

  function isMember(circle: Circle, personID: string) {
    return circle.members.some((member) => member.id === personID);
  }

  function interestEntry(personID: string) {
    return interest.data?.entries.find((entry) => entry.person.id === personID);
  }

  return (
    <section aria-labelledby="visibility-title" className="visibility-shell">
      <header className="visibility-header">
        <div>
          <p className="eyebrow">DISCOVERY, NOT ACCESS</p>
          <h2 id="visibility-title">Visibility and Interest lists</h2>
          <p>
            Circles control which People a Recipient can discover. Interest
            choices help prepare Audience proposals, but neither grants Media,
            search, Comment, archive, or notification access.
          </p>
        </div>
      </header>

      <div className="visibility-layout">
        <section aria-labelledby="circles-title" className="people-panel">
          <div className="family-panel-heading">
            <h3 id="circles-title">Visibility circles</h3>
          </div>
          <form className="visibility-circle-form" onSubmit={submitCircle}>
            <label>
              {editingCircle ? "Circle name" : "New circle name"}
              <input
                maxLength={120}
                onChange={(event) => setCircleName(event.target.value)}
                required
                value={circleName}
              />
            </label>
            <div className="visibility-actions">
              <button
                disabled={saveCircle.isPending || membership.isPending}
                type="submit"
              >
                {editingCircle ? "Save circle" : "Create circle"}
              </button>
              {editingCircle ? (
                <>
                  <button
                    disabled={membership.isPending}
                    onClick={() => {
                      setEditingCircleID("");
                      setCircleName("");
                    }}
                    type="button"
                  >
                    Cancel
                  </button>
                  <button
                    className="danger-button"
                    disabled={archiveCircle.isPending || membership.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Archive ${editingCircle.name}? Choices that lose visibility will become inactive.`,
                        )
                      ) {
                        archiveCircle.mutate(editingCircle);
                      }
                    }}
                    type="button"
                  >
                    Archive circle
                  </button>
                </>
              ) : null}
            </div>
          </form>
          <ErrorNotice error={saveCircle.error ?? archiveCircle.error} />
          <div className="visibility-circle-list">
            {activeCircles.map((circle) => (
              <button
                aria-label={`Edit ${circle.name}`}
                key={circle.id}
                onClick={() => startEditing(circle)}
                type="button"
              >
                <strong>{circle.name}</strong>
                <span>
                  {circle.members.length} direct{" "}
                  {circle.members.length === 1 ? "member" : "members"}
                </span>
              </button>
            ))}
            {circles.isSuccess && activeCircles.length === 0 ? (
              <p>No Visibility circles yet.</p>
            ) : null}
          </div>
        </section>

        <section
          aria-labelledby="membership-title"
          className="people-panel visibility-membership-panel"
        >
          <h3 id="membership-title">Circle membership</h3>
          <p>
            People can belong to several circles. Discovery is the direct union
            of shared circles and never crosses through an intermediary.
          </p>
          <ErrorNotice
            error={people.error ?? circles.error ?? membership.error}
          />
          {circles.isPending || people.isPending ? (
            <p>Loading Visibility circles and People…</p>
          ) : activeCircles.length ? (
            <>
              <div className="visibility-matrix-wrap">
                <table className="visibility-matrix">
                  <thead>
                    <tr>
                      <th scope="col">Person</th>
                      {activeCircles.map((circle) => (
                        <th key={circle.id} scope="col">
                          {circle.name}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {currentPeople.map((person) => (
                      <tr key={person.id}>
                        <th scope="row">{person.display_name}</th>
                        {activeCircles.map((circle) => (
                          <td key={circle.id}>
                            <input
                              aria-label={`${person.display_name} in ${circle.name}`}
                              checked={isMember(circle, person.id)}
                              disabled={membership.isPending}
                              onChange={(event) =>
                                membership.mutate({
                                  circle,
                                  person,
                                  included: event.target.checked,
                                })
                              }
                              type="checkbox"
                            />
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="visibility-mobile-list">
                <label>
                  Circle
                  <select
                    onChange={(event) => setMobileCircleID(event.target.value)}
                    value={mobileCircle?.id ?? ""}
                  >
                    {activeCircles.map((circle) => (
                      <option key={circle.id} value={circle.id}>
                        {circle.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Filter People
                  <input
                    onChange={(event) => setMemberFilter(event.target.value)}
                    placeholder="Name or sort name"
                    type="search"
                    value={memberFilter}
                  />
                </label>
                <div className="visibility-filtered-members">
                  {filteredPeople.map((person) => (
                    <label className="choice" key={person.id}>
                      <input
                        checked={Boolean(
                          mobileCircle && isMember(mobileCircle, person.id),
                        )}
                        disabled={!mobileCircle || membership.isPending}
                        onChange={(event) => {
                          if (mobileCircle) {
                            membership.mutate({
                              circle: mobileCircle,
                              person,
                              included: event.target.checked,
                            });
                          }
                        }}
                        type="checkbox"
                      />
                      <span>{person.display_name}</span>
                    </label>
                  ))}
                </div>
              </div>
            </>
          ) : circles.isSuccess && people.isSuccess ? (
            <p>Create a circle before assigning membership.</p>
          ) : null}
        </section>

        <section
          aria-labelledby="interest-title"
          className="people-panel visibility-interest-panel"
        >
          <h3 id="interest-title">Interest list editor</h3>
          <p>
            Edit a Recipient's private list on their behalf. It starts empty,
            excludes their own Person, and preserves inactive choices and a
            retained mutation history.
          </p>
          <label>
            Recipient
            <select
              disabled={mutateInterest.isPending || loadHistory.isPending}
              onChange={(event) => setRecipientID(event.target.value)}
              value={recipientID}
            >
              <option value="">Choose a Recipient</option>
              {recipients.map((person) => (
                <option key={person.id} value={person.id}>
                  {person.display_name}
                </option>
              ))}
            </select>
          </label>
          <ErrorNotice
            error={
              interest.error ??
              discoverable.error ??
              mutateInterest.error ??
              loadHistory.error
            }
          />
          {recipientID && interest.isSuccess && discoverable.isSuccess ? (
            <div className="interest-editor">
              <h4>Interest choices</h4>
              {interestChoices.length ? (
                <div className="interest-choices">
                  {interestChoices.map((person) => (
                    <InterestChoice
                      currentlyDiscoverable={discoverable.data.people.some(
                        (choice) => choice.id === person.id,
                      )}
                      entry={interestEntry(person.id)}
                      key={person.id}
                      onChange={(selected) =>
                        mutateInterest.mutate({
                          recipientID,
                          person,
                          selected,
                        })
                      }
                      pending={mutateInterest.isPending}
                      person={person}
                    />
                  ))}
                </div>
              ) : (
                <p>No People are discoverable through shared circles.</p>
              )}
              <InterestHistory
                interest={interest.data}
                onLoadMore={(cursor) =>
                  loadHistory.mutate({ cursor, recipientID })
                }
                pending={loadHistory.isPending}
              />
            </div>
          ) : null}
        </section>
      </div>
    </section>
  );
}
