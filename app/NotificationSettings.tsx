import { useState } from "react";

import {
  useEmailPreferences,
  usePlatformEmailDefaults,
  useUpdateEmailPreferences,
  useUpdatePlatformEmailDefaults,
} from "./hooks/queries/notifications";
import { useRebasedDraft } from "./hooks/useRebasedDraft";
import { ErrorMessage } from "./presentation";
import type {
  EmailPreferenceRequest,
  PlatformEmailDefaultsRequest,
} from "./types/generated/recipients";
import type { SessionResponse } from "./types/generated/setup";

export function EmailPreferences({ session }: { session: SessionResponse }) {
  const [requested, setRequested] = useState(false);
  const preferences = useEmailPreferences(session.csrf_token, requested);
  const {
    draft,
    setDraft,
    acceptServerDraft,
    hasStaleConflict,
    resetToServer,
  } = useRebasedDraft<EmailPreferenceRequest>(preferences.data);
  const update = useUpdateEmailPreferences(
    session.csrf_token,
    acceptServerDraft,
  );

  if (!requested) {
    return (
      <section aria-labelledby="email-preferences-title" className="shell-card">
        <h2 id="email-preferences-title">Optional email</h2>
        <p>
          Email choices are independent of private Media access and required
          identity or security email.
        </p>
        <button onClick={() => setRequested(true)} type="button">
          Manage email preferences
        </button>
      </section>
    );
  }
  if (preferences.isPending)
    return <p aria-live="polite">Loading email preferences…</p>;
  if (preferences.isError || !draft)
    return <ErrorMessage error={preferences.error} />;
  const busy = update.isPending;
  return (
    <section aria-labelledby="email-preferences-title" className="shell-card">
      <h2 id="email-preferences-title">Optional email</h2>
      <p>
        Choose Immediate, Weekly, or None without changing Media access or your
        required identity and security email.
      </p>
      <form
        className="setup-form"
        onSubmit={(event) => {
          event.preventDefault();
          update.mutate(draft);
        }}
      >
        <label>
          Publication and Comment email
          <select
            disabled={busy}
            onChange={(event) =>
              setDraft({ ...draft, email_preference: event.target.value })
            }
            value={draft.email_preference}
          >
            <option value="immediate">Immediate</option>
            <option value="weekly">Weekly digest</option>
            <option value="none">None</option>
          </select>
        </label>
        <fieldset disabled={busy || draft.email_preference !== "weekly"}>
          <legend>Weekly schedule</legend>
          <label>
            Day
            <select
              onChange={(event) =>
                setDraft({ ...draft, weekly_day: event.target.value })
              }
              value={draft.weekly_day}
            >
              {[
                "sunday",
                "monday",
                "tuesday",
                "wednesday",
                "thursday",
                "friday",
                "saturday",
              ].map((day) => (
                <option key={day} value={day}>
                  {day[0].toUpperCase() + day.slice(1)}
                </option>
              ))}
            </select>
          </label>
          <label>
            Local time
            <input
              onChange={(event) =>
                setDraft({ ...draft, weekly_local_time: event.target.value })
              }
              required
              type="time"
              value={draft.weekly_local_time}
            />
          </label>
          <label>
            Timezone
            <input
              autoComplete="off"
              onChange={(event) =>
                setDraft({ ...draft, weekly_timezone: event.target.value })
              }
              placeholder="America/New_York"
              required
              value={draft.weekly_timezone}
            />
          </label>
        </fieldset>
        <ErrorMessage error={update.error} />
        {hasStaleConflict ? (
          <div className="form-error" role="alert">
            <p>
              These preferences changed elsewhere while you were editing. Your
              edits are preserved.
            </p>
            <button onClick={resetToServer} type="button">
              Reload latest preferences
            </button>
          </div>
        ) : null}
        {update.isSuccess ? (
          <p aria-live="polite">Your email preferences were saved.</p>
        ) : null}
        <button disabled={busy} type="submit">
          {busy ? "Saving…" : "Save email preferences"}
        </button>
      </form>
    </section>
  );
}

export function PlatformEmailDefaults({
  session,
}: {
  session: SessionResponse;
}) {
  const [requested, setRequested] = useState(false);
  const defaults = usePlatformEmailDefaults(session.csrf_token, requested);
  const {
    draft,
    setDraft,
    acceptServerDraft,
    hasStaleConflict,
    resetToServer,
  } = useRebasedDraft<PlatformEmailDefaultsRequest>(
    defaults.data
      ? { weekly_timezone: defaults.data.weekly_timezone }
      : undefined,
  );
  const update = useUpdatePlatformEmailDefaults(
    session.csrf_token,
    (response) =>
      acceptServerDraft({ weekly_timezone: response.weekly_timezone }),
  );
  if (!requested) {
    return (
      <section
        aria-labelledby="platform-email-defaults-title"
        className="shell-card"
      >
        <h2 id="platform-email-defaults-title">Household weekly default</h2>
        <p>
          Recipients without a personal override use Sunday at 9:00 AM in this
          timezone.
        </p>
        <button onClick={() => setRequested(true)} type="button">
          Configure household timezone
        </button>
      </section>
    );
  }
  if (defaults.isPending)
    return <p aria-live="polite">Loading the household email default…</p>;
  if (defaults.isError) return <ErrorMessage error={defaults.error} />;
  if (!draft) return null;
  const currentTimezone = draft.weekly_timezone;
  return (
    <section
      aria-labelledby="platform-email-defaults-title"
      className="shell-card"
    >
      <h2 id="platform-email-defaults-title">Household weekly default</h2>
      <form
        className="setup-form"
        onSubmit={(event) => {
          event.preventDefault();
          update.mutate({ weekly_timezone: currentTimezone });
        }}
      >
        <p>
          Sunday at 9:00 AM for Recipients who have not chosen a personal
          schedule.
        </p>
        <label>
          Default timezone
          <input
            disabled={update.isPending}
            onChange={(event) =>
              setDraft({ weekly_timezone: event.target.value })
            }
            placeholder="America/New_York"
            required
            value={currentTimezone}
          />
        </label>
        <ErrorMessage error={update.error} />
        {hasStaleConflict ? (
          <div className="form-error" role="alert">
            <p>
              This timezone changed elsewhere while you were editing. Your edit
              is preserved.
            </p>
            <button onClick={resetToServer} type="button">
              Reload latest timezone
            </button>
          </div>
        ) : null}
        {update.isSuccess ? (
          <p aria-live="polite">The household weekly default was saved.</p>
        ) : null}
        <button disabled={update.isPending} type="submit">
          {update.isPending ? "Saving…" : "Save household default"}
        </button>
      </form>
    </section>
  );
}
