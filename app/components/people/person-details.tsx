import { useState } from "react";

import {
  usePreauthorize,
  useRevokePreauthorization,
  useUnlinkPersonIdentity,
  useUpdatePerson,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { formatDate } from "../../lib/utils";
import type {
  PersonDetail,
  UpdatePersonRequest,
} from "../../types/generated/identity";
import { LinkedIdentities } from "../identity/linked-identities";
import { Button } from "../ui/button";
import { ConfirmAction } from "./confirm-action";
import {
  CheckField,
  Field,
  Form,
  headingClass,
  sectionHeadingClass,
} from "./form-fields";

export function PersonDetails({ detail }: { detail: PersonDetail }) {
  const { person } = detail;
  const initial = {
    display_name: person.display_name,
    is_curator: person.is_curator,
    deactivated: !!person.deactivated_at,
  };
  const [draft, setDraft] = useState<UpdatePersonRequest | null>(null);
  const values = draft ?? initial;
  const [email, setEmail] = useState("");
  const update = useUpdatePerson(person.id);
  const preauthorize = usePreauthorize(person.id);
  const revoke = useRevokePreauthorization(person.id);
  const unlink = useUnlinkPersonIdentity(person.id);
  const dirty =
    draft !== null && JSON.stringify(draft) !== JSON.stringify(initial);
  useUnsavedChanges(
    dirty || !!email || update.isPending || preauthorize.isPending,
  );
  const errors = fieldErrors(update.error);
  return (
    <>
      <h1 className={headingClass}>{person.display_name}</h1>
      {person.deactivated_at && (
        <p className="mt-3 text-destructive">Deactivated</p>
      )}
      <div className="mt-9 grid gap-x-16 min-[961px]:grid-cols-[minmax(0,380px)_minmax(0,650px)]">
        <section className="pb-9">
          <h2 className={sectionHeadingClass}>Person details</h2>
          <Form
            aria-busy={update.isPending}
            aria-label="Edit person"
            className="mt-6 max-w-110"
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
              <CheckField
                checked={values.is_curator}
                error={errors.is_curator}
                name="is_curator"
                onChange={(event) => {
                  update.reset();
                  setDraft({ ...values, is_curator: event.target.checked });
                }}
              >
                Curator
              </CheckField>
              <p className="mb-6 text-xs text-muted">
                Curators can manage people and choose what to share.
              </p>
              <CheckField
                checked={values.deactivated}
                error={errors.deactivated}
                name="deactivated"
                onChange={(event) => {
                  update.reset();
                  setDraft({ ...values, deactivated: event.target.checked });
                }}
              >
                Deactivate this person
              </CheckField>
              <p className="mb-6 text-xs text-muted">
                Deactivation signs them out everywhere and prevents sign-in.
                Clear this option to restore access.
              </p>
              <Button type="submit">
                {update.isPending ? "Saving…" : "Save person"}
              </Button>
            </fieldset>
            {update.isSuccess && (
              <p className="mt-4 text-sm text-muted" role="status">
                Person saved.
              </p>
            )}
          </Form>
        </section>
        <div>
          <section className="border-t border-border py-8 min-[961px]:border-0 min-[961px]:pt-0">
            <h2 className={sectionHeadingClass}>Preauthorizations</h2>
            <p className="mt-3 mb-6 max-w-150 text-sm text-muted">
              Enter the exact Google email address, including uppercase and
              lowercase letters. Signing in with that address links it to this
              person. Approval does not expire and does not send an email.
            </p>
            <Form
              aria-busy={preauthorize.isPending}
              aria-label="Preauthorize email"
              className="max-w-110"
              error={preauthorize.error}
              onSubmit={(event) => {
                event.preventDefault();
                if (!preauthorize.isPending)
                  preauthorize.mutate(
                    { email },
                    { onSuccess: () => setEmail("") },
                  );
              }}
            >
              <fieldset disabled={preauthorize.isPending}>
                <Field
                  autoCapitalize="none"
                  autoCorrect="off"
                  error={fieldErrors(preauthorize.error).email}
                  label="Google email address"
                  maxLength={254}
                  name="email"
                  onChange={(event) => {
                    preauthorize.reset();
                    setEmail(event.target.value);
                  }}
                  required
                  type="email"
                  value={email}
                />
                <Button type="submit">
                  {preauthorize.isPending ? "Approving…" : "Preauthorize email"}
                </Button>
              </fieldset>
            </Form>
            {detail.preauthorizations.length ? (
              <ul className="mt-6 divide-y divide-border">
                {detail.preauthorizations.map((authorization) => (
                  <li
                    className="flex flex-wrap items-center justify-between gap-4 py-5"
                    key={authorization.id}
                  >
                    <div className="min-w-0">
                      <p className="wrap-anywhere">{authorization.email}</p>
                      <p className="mt-2 text-sm text-muted">
                        {authorization.consumed_at
                          ? "Consumed"
                          : authorization.revoked_at
                            ? "Revoked"
                            : "Unused"}
                      </p>
                      <p className="mt-2 text-xs text-muted">
                        Approved {formatDate(authorization.created_at)}
                      </p>
                    </div>
                    {!authorization.consumed_at &&
                      !authorization.revoked_at && (
                        <ConfirmAction
                          description="This unused approval will no longer grant sign-in access."
                          error={revoke.error}
                          label={`Revoke ${authorization.email}`}
                          onConfirm={() => revoke.mutateAsync(authorization.id)}
                          pending={revoke.isPending}
                        />
                      )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-6 text-sm text-muted">
                No email addresses approved yet.
              </p>
            )}
          </section>
          <LinkedIdentities
            error={unlink.error}
            identities={detail.identities}
            pending={unlink.isPending}
            unlink={unlink.mutateAsync}
          />
        </div>
      </div>
    </>
  );
}
