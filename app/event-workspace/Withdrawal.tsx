import { useEffect, useState } from "react";

import {
  useWithdrawEvent,
  type WithdrawEventAttempt,
} from "../hooks/queries/events";
import type {
  Event,
  Withdrawal as WithdrawalResult,
  WithdrawalTarget,
} from "../types/generated/events";

type WithdrawalProps = {
  event: Event;
  identityGeneration: string;
  revision: number;
  selectionGeneration: number;
  resetKey: string | number;
  saveIsSaved: boolean;
  onStarted: (attempt: WithdrawEventAttempt) => void;
  onCommitted: (attempt: WithdrawEventAttempt) => void;
  onAuthorityUncertain: (attempt: WithdrawEventAttempt) => void;
  onWithdrawn: (
    withdrawal: WithdrawalResult,
    attempt: WithdrawEventAttempt,
    authoritativeEvent: Event | undefined,
  ) => void;
  onBusyChange: (busy: boolean) => void;
};

export function Withdrawal({ resetKey, ...props }: WithdrawalProps) {
  return <WithdrawalState key={resetKey} {...props} />;
}

function WithdrawalState({
  event,
  identityGeneration,
  revision,
  selectionGeneration,
  saveIsSaved,
  onStarted,
  onCommitted,
  onAuthorityUncertain,
  onWithdrawn,
  onBusyChange,
}: Omit<WithdrawalProps, "resetKey">) {
  const [target, setTarget] = useState<WithdrawalTarget>();
  const [reason, setReason] = useState("");
  const withdraw = useWithdrawEvent(identityGeneration, {
    onStarted: (attempt) => {
      onBusyChange(true);
      onStarted(attempt);
    },
    onCommitted: (_withdrawal, attempt) => onCommitted(attempt),
    onSuccess: (withdrawal, attempt, authoritativeEvent) => {
      setTarget(undefined);
      setReason("");
      onWithdrawn(withdrawal, attempt, authoritativeEvent);
    },
    onError: (_error, attempt) => onAuthorityUncertain(attempt),
  });
  const targets = event.withdrawal_targets;
  const selectedTarget =
    target &&
    targets.some(
      (candidate) =>
        candidate.target_kind === target.target_kind &&
        candidate.target_id === target.target_id,
    )
      ? target
      : targets[0];

  useEffect(() => {
    onBusyChange(withdraw.isPending);
    return () => onBusyChange(false);
  }, [onBusyChange, withdraw.isPending]);

  if (event.lifecycle !== "published") return null;
  return (
    <section
      aria-labelledby="withdrawal-actions-title"
      className="publication-actions withdrawal-actions"
    >
      <h4 id="withdrawal-actions-title">Withdraw access</h4>
      <p>
        Withdrawal takes effect immediately. Restoration is not a toggle and
        requires newly reviewed Audiences plus a fresh Publication for every
        Event where the identity is currently placed. Reused Media may require
        several Publications.
      </p>
      <label>
        Currently published target
        <select
          disabled={targets.length === 0}
          onChange={(input) => {
            setTarget(
              targets.find(
                (candidate) => candidate.target_id === input.target.value,
              ),
            );
            withdraw.reset();
          }}
          value={selectedTarget?.target_id ?? ""}
        >
          {targets.length === 0 ? (
            <option value="">No targets available</option>
          ) : null}
          {targets.map((candidate) => (
            <option
              key={`${candidate.target_kind}:${candidate.target_id}`}
              value={candidate.target_id}
            >
              {candidate.label}
            </option>
          ))}
        </select>
      </label>
      <label>
        Attributable reason
        <textarea
          maxLength={1000}
          onChange={(input) => setReason(input.target.value)}
          required
          value={reason}
        />
      </label>
      <button
        disabled={
          !saveIsSaved ||
          withdraw.isPending ||
          !selectedTarget ||
          !reason.trim()
        }
        onClick={() => {
          if (
            selectedTarget &&
            window.confirm(
              "Withdraw Recipient access immediately? Identity and history will be preserved.",
            )
          )
            withdraw.mutate({
              target: selectedTarget,
              reason,
              event,
              revision,
              selectionGeneration,
            });
        }}
        type="button"
      >
        {withdraw.isPending ? "Withdrawing…" : "Withdraw access"}
      </button>
      {withdraw.isError ? (
        <p className="form-error" role="alert">
          {withdraw.error.message}
        </p>
      ) : null}
      {withdraw.data ? (
        <p role="status">
          Access withdrawn for {withdraw.data.affected_recipient_count}{" "}
          Recipients across {withdraw.data.affected_media_count} Media items.
          Withdrawal created no new external notification. A delivery already
          handed off before it committed may still arrive.
        </p>
      ) : null}
      {event.withdrawals.length > 0 ? (
        <div>
          <h5>Withdrawal history</h5>
          <ul>
            {event.withdrawals.map((item) => (
              <li key={item.id}>
                <strong>{item.target_kind}</strong>: {item.reason} by{" "}
                {item.withdrawn_by_name}.{" "}
                {item.restored_at
                  ? "Restored by a later Publication."
                  : "Access remains withdrawn."}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}
