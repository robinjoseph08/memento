import { useId, useState } from "react";

import { useIdentityStatus, useSignOut } from "../../hooks/queries/identity";
import {
  useProfile,
  useSessions,
  useUnlinkIdentity,
  useUpdateProfile,
} from "../../hooks/queries/profile";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  Profile,
  UpdateProfileRequest,
} from "../../types/generated/identity";
import { ConfirmAction } from "../people/confirm-action";
import {
  CheckField,
  Field,
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
  selectClass,
} from "../people/form-fields";
import { Button } from "../ui/button";
import { LinkedIdentities } from "./linked-identities";
import { SessionTable } from "./session-table";

export function ProfilePage() {
  const profile = useProfile();
  return (
    <div className="mx-auto max-w-280">
      <h1 className={headingClass}>Your profile</h1>
      {profile.isPending && (
        <p className="mt-6" role="status">
          Loading profile…
        </p>
      )}
      {profile.isError && (
        <ReadFailure
          error={profile.error}
          pending={profile.isFetching}
          retry={profile.refetch}
        />
      )}
      {profile.data && <ProfileDetails profile={profile.data} />}
    </div>
  );
}

function ProfileDetails({ profile }: { profile: Profile }) {
  const initial = {
    display_name: profile.person.display_name,
    update_email: profile.person.update_email,
    email_updates: profile.person.email_updates,
  };
  const [draft, setDraft] = useState<UpdateProfileRequest | null>(null);
  const values = draft ?? initial;
  const update = useUpdateProfile();
  const unlink = useUnlinkIdentity();
  const dirty =
    draft !== null && JSON.stringify(draft) !== JSON.stringify(initial);
  useUnsavedChanges(dirty || update.isPending);
  const errors = fieldErrors(update.error);
  const emailId = useId();
  const emails = [
    ...new Set(profile.identities.map((identity) => identity.email)),
  ];
  return (
    <div className="mt-9 grid min-w-0 gap-x-12 min-[1201px]:grid-cols-[minmax(0,320px)_minmax(0,1fr)]">
      <Form
        aria-busy={update.isPending}
        aria-label="Edit profile"
        className="max-w-110 pb-9"
        error={update.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (!update.isPending)
            update.mutate(values, { onSuccess: () => setDraft(null) });
        }}
      >
        <fieldset disabled={update.isPending}>
          <Field
            error={errors.display_name}
            label="Display name"
            maxLength={100}
            name="display_name"
            onChange={(event) => {
              update.reset();
              setDraft({ ...values, display_name: event.target.value });
            }}
            required
            value={values.display_name}
          />
          <div className="mb-5">
            <label className="mb-2 block text-xs font-medium" htmlFor={emailId}>
              Email for updates
            </label>
            <select
              aria-describedby={
                errors.update_email ? `${emailId}-error` : undefined
              }
              aria-invalid={!!errors.update_email}
              className={selectClass}
              id={emailId}
              name="update_email"
              onChange={(event) => {
                update.reset();
                setDraft({ ...values, update_email: event.target.value });
              }}
              value={values.update_email}
            >
              <option value="">Choose an email address</option>
              {emails.map((email) => (
                <option key={email} value={email}>
                  {email}
                </option>
              ))}
            </select>
            <FieldError error={errors.update_email} id={`${emailId}-error`} />
          </div>
          <CheckField
            checked={values.email_updates}
            error={errors.email_updates}
            name="email_updates"
            onChange={(event) => {
              update.reset();
              setDraft({ ...values, email_updates: event.target.checked });
            }}
          >
            Email me when there are updates
          </CheckField>
          <Button type="submit">
            {update.isPending ? "Saving…" : "Save profile"}
          </Button>
        </fieldset>
        {update.isSuccess && (
          <p className="mt-4 text-sm text-muted" role="status">
            Profile saved.
          </p>
        )}
      </Form>
      <div className="min-w-0 min-[1201px]:[&>section:first-child]:border-0 min-[1201px]:[&>section:first-child]:pt-0">
        <LinkedIdentities
          canUnlinkLast={false}
          error={unlink.error}
          identities={profile.identities}
          pending={unlink.isPending}
          unlink={unlink.mutateAsync}
        />
        <Sessions />
      </div>
    </div>
  );
}

function Sessions() {
  const sessions = useSessions();
  const { data: identity } = useIdentityStatus();
  const signOut = useSignOut(true);
  return (
    <section
      aria-labelledby="browser-sessions"
      className="border-t border-border py-8"
    >
      <h2 className={sectionHeadingClass} id="browser-sessions">
        Browser sessions
      </h2>
      <p className="mt-3 mb-6 text-sm text-muted">
        Browsers currently signed in to your account.
      </p>
      {sessions.isPending && <p role="status">Loading sessions…</p>}
      {sessions.isError && (
        <ReadFailure
          error={sessions.error}
          pending={sessions.isFetching}
          retry={sessions.refetch}
        />
      )}
      {sessions.data && (
        <div className="mb-6">
          <SessionTable
            sessions={sessions.data}
            showExpiration={!!identity?.person?.is_curator}
          />
        </div>
      )}
      <ConfirmAction
        description="You'll be signed out of every browser, including this one. Unsaved profile changes will be lost. Sign in again with a linked account to return."
        error={signOut.error}
        label="Sign out everywhere"
        onConfirm={() => signOut.mutateAsync()}
        pending={signOut.isPending}
      />
    </section>
  );
}
