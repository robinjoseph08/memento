import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiJSON } from "./api";
import type { ListResponse as PeopleListResponse } from "./types/generated/people";
import type {
  FaceAnchorEvidence,
  LinkPersonRequest,
  ListResponse,
  MediaCandidate,
  MutationResponse,
  PersonCandidate,
  SourceProblem,
  UnlinkedPerson,
} from "./types/generated/repairs";

function EvidenceField({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value || "Unavailable"}</dd>
    </div>
  );
}

function CandidateActions({
  kind,
  candidateID,
  candidateLabel,
  canConfirm = true,
  confirmText = "Confirm repair",
  csrfToken,
  reviewToken,
}: {
  kind: "people" | "media";
  candidateID: string;
  candidateLabel: string;
  canConfirm?: boolean;
  confirmText?: string;
  csrfToken: string;
  reviewToken?: string;
}) {
  const queryClient = useQueryClient();
  const [action, setAction] = useState<"confirm" | "reject">();
  const resolve = useMutation({
    mutationFn: (nextAction: "confirm" | "reject") => {
      setAction(nextAction);
      return apiJSON<MutationResponse>(
        `/api/repairs/${kind}/${candidateID}/${nextAction}`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Memento-CSRF": csrfToken,
          },
          body:
            kind === "media"
              ? JSON.stringify({ review_token: reviewToken })
              : undefined,
        },
      );
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["repairs"] });
    },
  });
  return (
    <>
      {resolve.error ? (
        <p className="form-error" role="alert">
          {resolve.error.message}
        </p>
      ) : null}
      {resolve.data ? (
        <p role="status">
          Repair{" "}
          {resolve.data.status === "confirmed" ? "confirmed" : "rejected"}.
        </p>
      ) : null}
      <p>
        {kind === "people"
          ? "Rejecting keeps Attendance suggestions suppressed and permanently dismisses this exact proposal."
          : "Rejecting keeps the addition and removal separate and permanently dismisses this exact proposal."}
      </p>
      <div className="repair-actions">
        {canConfirm ? (
          <button
            aria-label={`Confirm repair for ${candidateLabel}`}
            disabled={resolve.isPending}
            onClick={() => resolve.mutate("confirm")}
            type="button"
          >
            {resolve.isPending && action === "confirm"
              ? "Confirming…"
              : confirmText}
          </button>
        ) : null}
        <button
          aria-label={`Reject repair for ${candidateLabel}`}
          disabled={resolve.isPending}
          onClick={() => resolve.mutate("reject")}
          type="button"
        >
          {resolve.isPending && action === "reject" ? "Rejecting…" : "Reject"}
        </button>
      </div>
    </>
  );
}

function Conflicts({
  values,
  pending,
}: {
  values: string[];
  pending: boolean;
}) {
  if (values.length === 0) {
    return <p className="repair-confidence">No conflicting evidence.</p>;
  }
  return (
    <div className="repair-conflicts">
      <strong>
        {pending ? "Conflicts requiring judgment" : "Conflict evidence"}
      </strong>
      <ul>
        {values.map((value) => (
          <li key={value}>{value.replaceAll("_", " ")}</li>
        ))}
      </ul>
    </div>
  );
}

function FaceAnchors({ values }: { values: FaceAnchorEvidence[] }) {
  if (values.length === 0) {
    return <p>No related face anchors.</p>;
  }
  return (
    <section className="repair-anchors">
      <h4>
        {values.length} related face{" "}
        {values.length === 1 ? "anchor" : "anchors"}
      </h4>
      <ol>
        {values.map((anchor) => (
          <li key={`${anchor.asset_id}:${anchor.face_id}`}>
            <dl className="repair-evidence">
              <EvidenceField label="Face ID" value={anchor.face_id} />
              <EvidenceField label="Asset ID" value={anchor.asset_id} />
              <EvidenceField label="Checksum" value={anchor.checksum} />
              <EvidenceField
                label="Last Immich Person"
                value={anchor.last_immich_person_id}
              />
              <EvidenceField
                label="Image dimensions"
                value={`${anchor.image_width} × ${anchor.image_height}`}
              />
              <EvidenceField
                label="Face bounds"
                value={`${anchor.x1}, ${anchor.y1} to ${anchor.x2}, ${anchor.y2}`}
              />
            </dl>
          </li>
        ))}
      </ol>
    </section>
  );
}

function SourceProblemCard({ problem }: { problem: SourceProblem }) {
  const mediaProblem = problem.kind === "media_item";
  return (
    <article className="repair-card repair-problem">
      <p className="step-label">
        {problem.published && mediaProblem
          ? "Published Media unavailable"
          : mediaProblem
            ? "Media source missing"
            : "Source album missing"}
      </p>
      <h3>{problem.label}</h3>
      <p>
        {problem.priority === "critical"
          ? mediaProblem
            ? "This published Media problem has the highest priority. Its listing and Audience remain unchanged, but Media delivery is blocked."
            : "This published Source album problem has the highest priority. Published Media listings and Audiences remain unchanged. Media items remain available unless their own backing is missing."
          : "This Source problem needs Curator review. No backing or authorization changes automatically."}
      </p>
      {mediaProblem ? (
        <p>
          {problem.candidate_count > 0
            ? `${problem.candidate_count} replacement ${problem.candidate_count === 1 ? "candidate is" : "candidates are"} ready for evidence review below.`
            : "No safe replacement candidate is available yet."}
        </p>
      ) : null}
      <small>
        Missing since {new Date(problem.missing_since).toLocaleString()}
      </small>
    </article>
  );
}

function PersonRepair({
  candidate,
  csrfToken,
}: {
  candidate: PersonCandidate;
  csrfToken: string;
}) {
  return (
    <article
      aria-labelledby={`person-repair-${candidate.id}`}
      className="repair-card"
    >
      <header>
        <div>
          <p className="step-label">Person link · {candidate.state}</p>
          <h3 id={`person-repair-${candidate.id}`}>{candidate.person_name}</h3>
        </div>
      </header>
      <dl className="repair-evidence">
        <EvidenceField
          label="Previous Immich Person"
          value={candidate.previous_immich_person_id}
        />
        <EvidenceField
          label="Proposed Immich Person"
          value={candidate.candidate_immich_person_name}
        />
        <EvidenceField
          label="Proposed Immich Person ID"
          value={candidate.candidate_immich_person_id}
        />
      </dl>
      <FaceAnchors values={candidate.face_anchors} />
      <Conflicts
        pending={candidate.state === "pending"}
        values={candidate.conflicts}
      />
      {candidate.state === "pending" ? (
        <CandidateActions
          candidateID={candidate.id}
          candidateLabel={candidate.person_name}
          canConfirm={
            Boolean(candidate.candidate_immich_person_id) ||
            candidate.previous_immich_person_present
          }
          confirmText={
            candidate.candidate_immich_person_id
              ? "Confirm repair"
              : "Confirm current link"
          }
          csrfToken={csrfToken}
          kind="people"
        />
      ) : null}
    </article>
  );
}

function MediaRepair({
  candidate,
  csrfToken,
}: {
  candidate: MediaCandidate;
  csrfToken: string;
}) {
  return (
    <article
      aria-labelledby={`media-repair-${candidate.id}`}
      className="repair-card"
    >
      <header>
        <div>
          <p className="step-label">Media backing · {candidate.state}</p>
          <h3 id={`media-repair-${candidate.id}`}>
            {candidate.candidate.filename || "Unnamed Media item"}
          </h3>
        </div>
      </header>
      <div className="repair-comparison">
        <section>
          <h4>Previous backing</h4>
          <dl className="repair-evidence">
            <EvidenceField
              label="Immich asset ID"
              value={candidate.previous_immich_asset_id}
            />
            <EvidenceField
              label="Checksum"
              value={candidate.previous.checksum}
            />
            <EvidenceField label="Capture" value={candidate.previous.capture} />
            <EvidenceField
              label="Filename"
              value={candidate.previous.filename}
            />
            <EvidenceField label="Path" value={candidate.previous.path} />
          </dl>
        </section>
        <section>
          <h4>Candidate backing</h4>
          <dl className="repair-evidence">
            <EvidenceField
              label="Immich asset ID"
              value={candidate.candidate_immich_asset_id}
            />
            <EvidenceField label="Media type" value={candidate.media_type} />
            <EvidenceField
              label="Checksum"
              value={candidate.candidate.checksum}
            />
            <EvidenceField
              label="Capture"
              value={candidate.candidate.capture}
            />
            <EvidenceField
              label="Filename"
              value={candidate.candidate.filename}
            />
            <EvidenceField label="Path" value={candidate.candidate.path} />
          </dl>
        </section>
      </div>
      <FaceAnchors values={candidate.face_anchors} />
      <Conflicts
        pending={candidate.state === "pending"}
        values={candidate.conflicts}
      />
      {candidate.state === "pending" ? (
        <CandidateActions
          candidateID={candidate.id}
          candidateLabel={
            candidate.candidate.filename || candidate.candidate_immich_asset_id
          }
          csrfToken={csrfToken}
          kind="media"
          reviewToken={candidate.review_token}
        />
      ) : null}
    </article>
  );
}

function ImmichPersonAddition({
  person,
  people,
  csrfToken,
}: {
  person: UnlinkedPerson;
  people: PeopleListResponse["people"];
  csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const currentPeople = people.filter(
    (candidate) => candidate.status === "current",
  );
  const [selectedPersonID, setSelectedPersonID] = useState("");
  const personID = currentPeople.some(
    (candidate) => candidate.id === selectedPersonID,
  )
    ? selectedPersonID
    : (currentPeople[0]?.id ?? "");
  const link = useMutation({
    mutationFn: (request: LinkPersonRequest) =>
      apiJSON<MutationResponse>("/api/repairs/people/link", {
        method: "POST",
        headers: { "X-Memento-CSRF": csrfToken },
        body: JSON.stringify(request),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["repairs"] });
    },
  });
  return (
    <article
      aria-labelledby={`person-addition-${person.immich_person_id}`}
      className="repair-card repair-addition"
    >
      <p className="step-label">New Immich identity · addition</p>
      <h3 id={`person-addition-${person.immich_person_id}`}>
        {person.name || "Unnamed Immich Person"}
      </h3>
      <dl className="repair-evidence">
        <EvidenceField
          label="Immich Person ID"
          value={person.immich_person_id}
        />
        <EvidenceField
          label="Visibility"
          value={person.hidden ? "Hidden" : "Visible"}
        />
      </dl>
      <p>
        This identity produces no Attendance suggestions until you explicitly
        link it to a Person.
      </p>
      <label>
        Portal Person
        <select
          onChange={(event) => setSelectedPersonID(event.target.value)}
          value={personID}
        >
          {currentPeople.map((candidate) => (
            <option key={candidate.id} value={candidate.id}>
              {candidate.display_name}
            </option>
          ))}
        </select>
      </label>
      {link.error ? (
        <p className="form-error" role="alert">
          {link.error.message}
        </p>
      ) : null}
      <button
        disabled={!personID || link.isPending}
        onClick={() =>
          link.mutate({
            person_id: personID,
            immich_person_id: person.immich_person_id,
          })
        }
        type="button"
      >
        {link.isPending ? "Linking…" : "Confirm Person link"}
      </button>
    </article>
  );
}

export function RepairWorkspace({ csrfToken }: { csrfToken: string }) {
  const queryClient = useQueryClient();
  const repairs = useQuery({
    queryKey: ["repairs"],
    queryFn: () => apiJSON<ListResponse>("/api/repairs"),
    retry: false,
  });
  const people = useQuery({
    queryKey: ["people", "repair-links"],
    queryFn: () => apiJSON<PeopleListResponse>("/api/people"),
    retry: false,
  });
  const reconcile = useMutation({
    mutationFn: () =>
      apiJSON<MutationResponse>("/api/repairs/reconcile", {
        method: "POST",
        headers: { "X-Memento-CSRF": csrfToken },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["repairs"] });
    },
  });
  const data = repairs.data;
  const sourceProblems = data?.source_problems ?? [];
  const empty =
    data &&
    sourceProblems.length === 0 &&
    data.person_candidates.length === 0 &&
    data.media_candidates.length === 0 &&
    data.unlinked_immich_people.length === 0;

  return (
    <section aria-labelledby="repairs-title" className="repair-workspace">
      <header className="source-header">
        <div>
          <p className="step-label">Curator only</p>
          <h2 id="repairs-title">Immich identity repairs</h2>
          <p>
            Review private matching evidence. Confirmation changes only the
            Immich link or backing, never Recipient authorization or Audiences.
          </p>
        </div>
        <button
          disabled={reconcile.isPending}
          onClick={() => reconcile.mutate()}
          type="button"
        >
          {reconcile.isPending ? "Checking…" : "Check Person links"}
        </button>
      </header>
      {repairs.isPending ? <p>Loading repair evidence…</p> : null}
      {repairs.error || people.error || reconcile.error ? (
        <p className="form-error" role="alert">
          {(repairs.error ?? people.error ?? reconcile.error)?.message}
        </p>
      ) : null}
      {empty ? <p>No Immich identity changes need review.</p> : null}
      <div className="repair-list">
        {sourceProblems.map((problem) => (
          <SourceProblemCard
            key={`${problem.kind}:${problem.id}`}
            problem={problem}
          />
        ))}
        {data?.person_candidates.map((candidate) => (
          <PersonRepair
            candidate={candidate}
            csrfToken={csrfToken}
            key={candidate.id}
          />
        ))}
        {data?.media_candidates.map((candidate) => (
          <MediaRepair
            candidate={candidate}
            csrfToken={csrfToken}
            key={candidate.id}
          />
        ))}
        {data?.unlinked_immich_people.map((person) => (
          <ImmichPersonAddition
            csrfToken={csrfToken}
            key={person.immich_person_id}
            people={people.data?.people ?? []}
            person={person}
          />
        ))}
      </div>
    </section>
  );
}
