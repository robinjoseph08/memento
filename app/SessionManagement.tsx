import { useMemo, useState } from "react";

import {
  useCompleteEmailChange,
  useRenameSession,
  useRevokeSession,
  useSessions,
  useSignOutAllSessions,
  useStartEmailChange,
} from "./hooks/queries/sessions";
import { formatSourceDate } from "./format";
import { ErrorMessage } from "./presentation";
import type { EmailChangeStartResponse } from "./types/generated/sessions";
import type { SessionResponse } from "./types/generated/setup";

export function SessionManager({
  session,
  onSignedOut,
  mutationsDisabled = false,
}: {
  session: SessionResponse;
  onSignedOut: () => void;
  mutationsDisabled?: boolean;
}) {
  const [labels, setLabels] = useState<Record<string, string>>({});
  const [labelBases, setLabelBases] = useState<Record<string, string>>({});
  const [newEmail, setNewEmail] = useState("");
  const [emailChange, setEmailChange] = useState<EmailChangeStartResponse>();
  const [oldCode, setOldCode] = useState("");
  const [newCode, setNewCode] = useState("");
  const clearLabelDraft = (id: string) => {
    setLabels((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
    setLabelBases((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
  };
  const sessions = useSessions(session.csrf_token);
  const rename = useRenameSession(session.csrf_token, clearLabelDraft);
  const revoke = useRevokeSession(
    session.csrf_token,
    (id) =>
      Boolean(sessions.data?.sessions.find((item) => item.id === id)?.current),
    onSignedOut,
  );
  const signOutAll = useSignOutAllSessions(session.csrf_token, onSignedOut);
  const startEmailChange = useStartEmailChange(
    session.csrf_token,
    setEmailChange,
  );
  const completeEmailChange = useCompleteEmailChange(session.csrf_token);

  const staleLabels = useMemo(() => {
    const next: Record<string, boolean> = {};
    for (const item of sessions.data?.sessions ?? []) {
      const edited = labels[item.id];
      const base = labelBases[item.id];
      if (
        edited !== undefined &&
        base !== undefined &&
        edited !== base &&
        item.label !== base &&
        item.label !== edited
      ) {
        next[item.id] = true;
      }
    }
    return next;
  }, [labelBases, labels, sessions.data]);

  return (
    <details className="session-manager">
      <summary>Sessions and login email</summary>
      <p>
        Inspect and revoke each browser separately. Approximate location appears
        only when the operator configured a local GeoIP database.
      </p>
      {sessions.data?.sessions?.map((item) => (
        <article
          aria-label={`Session ${item.label || `${item.browser} on ${item.platform}`}`}
          className="session-row"
          key={item.id}
        >
          <div>
            <strong>
              {item.label || `${item.browser} on ${item.platform}`}
              {item.current ? " (this browser)" : ""}
            </strong>
            <span>
              {item.session_type === "public"
                ? "Public computer"
                : "Trusted device"}
              {` · ${item.status} · created ${formatSourceDate(item.created_at)} · last active ${formatSourceDate(item.last_activity_at)}`}
              {item.location ? ` · ${item.location}` : ""}
            </span>
            {!item.push_allowed ? <span>Push unavailable</span> : null}
          </div>
          <label>
            Session name
            <input
              aria-label={`Session name for ${item.label || `${item.browser} on ${item.platform}`}`}
              disabled={rename.isPending}
              onChange={(event) => {
                const nextLabel = event.target.value;
                const base = labelBases[item.id] ?? item.label;
                if (nextLabel === base) {
                  clearLabelDraft(item.id);
                  return;
                }
                setLabelBases((current) =>
                  current[item.id] === undefined
                    ? { ...current, [item.id]: item.label }
                    : current,
                );
                setLabels((current) => ({
                  ...current,
                  [item.id]: nextLabel,
                }));
              }}
              value={labels[item.id] ?? item.label}
            />
          </label>
          {staleLabels[item.id] ? (
            <div className="form-error" role="alert">
              <p>
                This Session name changed elsewhere. Your edit is preserved.
              </p>
              <button onClick={() => clearLabelDraft(item.id)} type="button">
                Reload latest Session name
              </button>
            </div>
          ) : null}
          <button
            aria-label={`Save name for ${item.label || `${item.browser} on ${item.platform}`}`}
            disabled={rename.isPending || item.status !== "active"}
            onClick={() =>
              rename.mutate({
                id: item.id,
                request: { label: labels[item.id] ?? item.label },
              })
            }
            type="button"
          >
            Save name
          </button>
          <button
            aria-label={`${item.current ? "Sign out" : "Revoke"} ${item.label || `${item.browser} on ${item.platform}`}`}
            className="danger-button"
            disabled={
              mutationsDisabled || revoke.isPending || item.status !== "active"
            }
            onClick={() => revoke.mutate(item.id)}
            type="button"
          >
            {item.current ? "Sign out this browser" : "Revoke Session"}
          </button>
        </article>
      ))}
      <ErrorMessage
        error={
          sessions.error ?? rename.error ?? revoke.error ?? signOutAll.error
        }
      />
      <button
        className="danger-button"
        disabled={mutationsDisabled || signOutAll.isPending}
        onClick={() => signOutAll.mutate()}
        type="button"
      >
        Sign out all Sessions
      </button>
      <div className="email-change">
        <h3>Change login email</h3>
        {!emailChange ? (
          <>
            <label>
              New login email
              <input
                onChange={(event) => setNewEmail(event.target.value)}
                type="email"
                value={newEmail}
              />
            </label>
            <button
              disabled={startEmailChange.isPending || !newEmail}
              onClick={() => startEmailChange.mutate({ new_email: newEmail })}
              type="button"
            >
              Send codes to both addresses
            </button>
          </>
        ) : (
          <>
            <p>Enter the fresh code sent to each address.</p>
            <label>
              Current-address code
              <input
                inputMode="numeric"
                onChange={(event) => setOldCode(event.target.value)}
                value={oldCode}
              />
            </label>
            <label>
              New-address code
              <input
                inputMode="numeric"
                onChange={(event) => setNewCode(event.target.value)}
                value={newCode}
              />
            </label>
            <button
              disabled={completeEmailChange.isPending}
              onClick={() =>
                completeEmailChange.mutate({
                  request_id: emailChange.request_id,
                  old_code: oldCode,
                  new_code: newCode,
                })
              }
              type="button"
            >
              Confirm email change
            </button>
          </>
        )}
        <ErrorMessage
          error={startEmailChange.error ?? completeEmailChange.error}
        />
      </div>
    </details>
  );
}
