import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { apiJSON } from "./api";
import type {
  ApprovalResponse,
  AttendanceRequest,
  OverrideRequest,
  Review,
} from "./types/generated/audiences";

const reasonLabels: Record<string, string> = {
  present: "Present",
  interested: "Interested",
  manually_included: "Manually included",
  manually_excluded: "Manually excluded",
};

export function AttendanceAudienceReview({
  momentID,
  csrfToken,
  onAttendanceConfirmed,
  onAudienceApproved,
}: {
  momentID: string;
  csrfToken: string;
  onAttendanceConfirmed: () => void;
  onAudienceApproved: () => void;
}) {
  const queryClient = useQueryClient();
  const queryKey = ["attendance-audience", momentID];
  const review = useQuery({
    queryKey,
    queryFn: () =>
      apiJSON<Review>(`/api/moments/${momentID}/attendance-audience`),
    retry: false,
  });
  const [attendanceOverride, setAttendanceOverride] = useState<Set<string>>();
  const [manualRecipientID, setManualRecipientID] = useState("");
  const attendance =
    attendanceOverride ??
    new Set(review.data?.attendance.map((person) => person.id) ?? []);

  const confirmAttendance = useMutation({
    mutationFn: () =>
      apiJSON<Review>(`/api/moments/${momentID}/attendance`, {
        method: "PUT",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": csrfToken,
        },
        body: JSON.stringify({
          person_ids: [...attendance],
        } satisfies AttendanceRequest),
      }),
    onSuccess: (result) => {
      queryClient.setQueryData(queryKey, result);
      onAttendanceConfirmed();
    },
  });
  const override = useMutation({
    mutationFn: (request: OverrideRequest) =>
      apiJSON<Review>(`/api/moments/${momentID}/audience/override`, {
        method: "PUT",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": csrfToken,
        },
        body: JSON.stringify(request),
      }),
    onSuccess: (result) => queryClient.setQueryData(queryKey, result),
  });
  const recalculate = useMutation({
    mutationFn: () =>
      apiJSON<Review>(`/api/moments/${momentID}/audience/recalculate`, {
        method: "POST",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": csrfToken,
        },
      }),
    onSuccess: (result) => queryClient.setQueryData(queryKey, result),
  });
  const approve = useMutation({
    mutationFn: () =>
      apiJSON<ApprovalResponse>(`/api/moments/${momentID}/audience/approve`, {
        method: "POST",
        headers: {
          "If-Match": String(review.data?.version ?? 0),
          "X-Memento-CSRF": csrfToken,
        },
      }),
    onSuccess: (result) => {
      queryClient.setQueryData<Review>(queryKey, (current) =>
        current
          ? {
              ...current,
              approved_audience: result.audience,
              version: result.version,
            }
          : current,
      );
      onAudienceApproved();
    },
  });

  const errors = [
    review.error,
    confirmAttendance.error,
    override.error,
    recalculate.error,
    approve.error,
  ].filter((error): error is Error => error instanceof Error);

  if (review.isPending) return <p>Loading Attendance and Audience…</p>;
  if (review.isError || !review.data)
    return (
      <div className="form-error" role="alert">
        <p>{review.error?.message ?? "The review could not be loaded."}</p>
        <button onClick={() => void review.refetch()} type="button">
          Retry review
        </button>
      </div>
    );

  const busy =
    confirmAttendance.isPending ||
    override.isPending ||
    recalculate.isPending ||
    approve.isPending;

  return (
    <div className="attendance-audience-review">
      {errors.length > 0 ? (
        <p className="form-error" role="alert">
          {errors.at(-1)?.message}
        </p>
      ) : null}
      <section aria-labelledby="face-evidence-title">
        <h4 id="face-evidence-title">Advisory face evidence</h4>
        {!review.data.face_evidence_available ? (
          <p>Face evidence is unavailable. Confirm Attendance manually.</p>
        ) : review.data.face_evidence.length === 0 ? (
          <p>
            No faces were suggested. Face evidence never confirms Attendance.
          </p>
        ) : (
          <ul>
            {review.data.face_evidence.map((evidence) => (
              <li key={evidence.evidence_id}>
                {evidence.suggested_person
                  ? `${evidence.suggested_person.display_name} suggested`
                  : "Unmatched face"}{" "}
                on Media {evidence.media_item_id.slice(0, 8)}. Review only, not
                access.
              </li>
            ))}
          </ul>
        )}
      </section>
      <fieldset disabled={busy}>
        <legend>Confirmed Attendance</legend>
        {review.data.people.map((person) => (
          <label className="inspection-check" key={person.id}>
            <input
              checked={attendance.has(person.id)}
              onChange={() =>
                setAttendanceOverride(() => {
                  const next = new Set(attendance);
                  if (next.has(person.id)) next.delete(person.id);
                  else next.add(person.id);
                  return next;
                })
              }
              type="checkbox"
            />
            {person.display_name}
          </label>
        ))}
        <button
          disabled={confirmAttendance.isPending}
          onClick={() => confirmAttendance.mutate()}
          type="button"
        >
          Confirm Attendance
        </button>
      </fieldset>
      <section aria-labelledby="proposal-title">
        <h4 id="proposal-title">Audience proposal</h4>
        <p>
          Proposals explain suggestions but never authorize Media. Manual
          choices survive recalculation.
        </p>
        {review.data.proposal.length === 0 ? (
          <p>No Recipients proposed.</p>
        ) : (
          <ul className="proposal-list">
            {review.data.proposal.map((proposal) => (
              <li key={proposal.recipient.id}>
                <label>
                  <input
                    checked={proposal.included}
                    disabled={busy}
                    onChange={(event) =>
                      override.mutate({
                        recipient_person_id: proposal.recipient.id,
                        state: event.target.checked ? "included" : "excluded",
                      })
                    }
                    type="checkbox"
                  />
                  {proposal.recipient.display_name}
                </label>
                <ul>
                  {proposal.reasons.map((reason, index) => (
                    <li
                      key={`${reason.kind}-${reason.matching_person?.id ?? index}`}
                    >
                      {reasonLabels[reason.kind] ?? reason.kind}
                      {reason.matching_person
                        ? `: ${reason.matching_person.display_name}`
                        : ""}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
        <div className="move-control">
          <label>
            Manually include Recipient
            <select
              disabled={busy}
              onChange={(event) => setManualRecipientID(event.target.value)}
              value={manualRecipientID}
            >
              <option value="">Choose an Eligible Recipient</option>
              {review.data.eligible_recipients.map((recipient) => (
                <option key={recipient.id} value={recipient.id}>
                  {recipient.display_name}
                </option>
              ))}
            </select>
          </label>
          <button
            disabled={busy || !manualRecipientID}
            onClick={() =>
              override.mutate({
                recipient_person_id: manualRecipientID,
                state: "included",
              })
            }
            type="button"
          >
            Include Recipient
          </button>
        </div>
        <div className="moment-actions">
          <button
            disabled={busy}
            onClick={() => recalculate.mutate()}
            type="button"
          >
            Recalculate proposal
          </button>
          <button
            disabled={busy}
            onClick={() => approve.mutate()}
            type="button"
          >
            {review.data.proposal.some((proposal) => proposal.included)
              ? "Approve Audience"
              : "Approve Curator only"}
          </button>
        </div>
        {review.data.approved_audience ? (
          <p>
            <strong>Approved snapshot:</strong>{" "}
            {review.data.approved_audience.label} (
            {review.data.approved_audience.recipients.length} Recipients). It
            will not recalculate later.
          </p>
        ) : (
          <p>No Audience approved yet.</p>
        )}
      </section>
    </div>
  );
}
