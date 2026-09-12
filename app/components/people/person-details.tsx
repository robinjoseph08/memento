import { ExternalLink } from "lucide-react";
import { useState } from "react";

import { useIdentityStatus } from "../../hooks/queries/identity";
import {
  usePreauthorize,
  useRevokePreauthorization,
  useSetPersonAvatar,
  useUnlinkPersonIdentity,
  useUpdatePerson,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { initials } from "../../lib/initials";
import type {
  PersonDetail,
  UpdatePersonRequest,
} from "../../types/generated/identity";
import { LinkedIdentities } from "../identity/linked-identities";
import { SessionTable } from "../identity/session-table";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import {
  CheckField,
  Failure,
  Field,
  FieldError,
  Form,
  headingClass,
  sectionHeadingClass,
} from "./form-fields";
import { PreauthorizationTables } from "./preauthorization-tables";

export function PersonDetails({ detail }: { detail: PersonDetail }) {
  const { person } = detail;
  const { data: identity } = useIdentityStatus();
  const isSelf = identity?.person?.id === person.id;
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
      <div className="mt-9 grid gap-x-12 min-[1201px]:grid-cols-[minmax(0,320px)_minmax(0,1fr)]">
        <div className="min-w-0 pb-8">
          <section className="pb-8">
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
                  disabled={isSelf}
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
                  disabled={isSelf}
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
                {isSelf && (
                  <p className="mb-6 text-xs text-muted">
                    You can't remove your own Curator role or deactivate
                    yourself.
                  </p>
                )}
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
          <AvatarEditor detail={detail} />
          <section
            aria-labelledby="notification-preferences"
            className="border-t border-border pt-8"
          >
            <h2 className={sectionHeadingClass} id="notification-preferences">
              Notifications
            </h2>
            <dl className="mt-6 space-y-5 text-sm">
              <div>
                <dt className="text-xs text-muted">Email for updates</dt>
                <dd className="mt-2 wrap-anywhere">
                  {person.update_email || "No email selected"}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted">Email updates</dt>
                <dd className="mt-2">
                  {person.email_updates ? "Subscribed" : "Not subscribed"}
                </dd>
              </div>
            </dl>
            <p className="mt-5 text-xs text-muted">
              This person manages their notification preferences in their
              profile.
            </p>
          </section>
        </div>
        <div className="min-w-0 min-[1201px]:[&>section:first-child]:border-0 min-[1201px]:[&>section:first-child]:pt-0">
          <LinkedIdentities
            canUnlinkLast={!!identity?.person && !isSelf}
            error={unlink.error}
            identities={detail.identities}
            pending={unlink.isPending}
            unlink={unlink.mutateAsync}
          />
          <section className="border-t border-border py-8">
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
            <PreauthorizationTables
              authorizations={detail.preauthorizations}
              error={revoke.error}
              pending={revoke.isPending}
              revoke={revoke.mutateAsync}
            />
          </section>
          <section
            aria-labelledby="browser-sessions"
            className="border-t border-border py-8"
          >
            <h2 className={sectionHeadingClass} id="browser-sessions">
              Browser sessions
            </h2>
            <p className="mt-3 mb-6 text-sm text-muted">
              Browsers currently signed in as this person.
            </p>
            <SessionTable
              sessions={detail.sessions ?? []}
              showExpiration={!!identity?.person?.is_curator}
            />
          </section>
        </div>
      </div>
    </>
  );
}

function AvatarEditor({ detail }: { detail: PersonDetail }) {
  const { person } = detail;
  const faces = detail.faces ?? [];
  const initial = faces.find((face) => face.avatar)?.source_face_id ?? "";
  const [selected, setSelected] = useState(initial);
  const update = useSetPersonAvatar(person.id);
  const dirty = selected !== initial;
  useUnsavedChanges(dirty || update.isPending);
  const errors = fieldErrors(update.error);

  return (
    <section
      aria-labelledby="person-avatar"
      className="border-t border-border py-8"
    >
      <h2 className={sectionHeadingClass} id="person-avatar">
        Avatar
      </h2>
      <div className="mt-5 flex items-center gap-4">
        <Avatar className="size-16 border border-border">
          {person.avatar_url && <AvatarImage alt="" src={person.avatar_url} />}
          <AvatarFallback className="text-lg">
            {initials(person.display_name)}
          </AvatarFallback>
        </Avatar>
        <p className="text-xs leading-relaxed text-muted">
          Choose one linked Immich face. If its thumbnail is unavailable,
          Memento shows initials.
        </p>
      </div>
      {faces.length === 0 ? (
        <p className="mt-5 text-sm text-muted">
          Open a Moment in an Album and link the face under its unlinked faces
          first.
        </p>
      ) : (
        <Form
          aria-busy={update.isPending}
          aria-label="Choose Person avatar"
          className="mt-5"
          error={update.error}
          onSubmit={(event) => {
            event.preventDefault();
            update.mutate({ source_face_id: selected });
          }}
        >
          <fieldset disabled={update.isPending}>
            <legend className="text-xs font-medium">Linked faces</legend>
            <div className="mt-3 flex flex-wrap gap-3">
              {faces.map((face) => (
                <div
                  className="flex flex-col items-center text-center text-xs"
                  key={face.source_face_id}
                >
                  <label className="cursor-pointer">
                    <input
                      aria-label={`Use ${face.source_name || "this face"} as avatar`}
                      checked={selected === face.source_face_id}
                      className="peer sr-only"
                      name="avatar_face"
                      onChange={() => {
                        setSelected(face.source_face_id);
                        update.reset();
                      }}
                      type="radio"
                      value={face.source_face_id}
                    />
                    <span className="inline-block rounded-full peer-checked:outline-2 peer-checked:outline-offset-2 peer-checked:outline-primary peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-ring">
                      <Avatar className="size-14">
                        <AvatarImage alt="" src={face.thumbnail_url} />
                        <AvatarFallback>
                          {initials(person.display_name)}
                        </AvatarFallback>
                      </Avatar>
                    </span>
                    <span className="mt-2 block max-w-20 truncate">
                      {face.source_name || "Immich face"}
                    </span>
                  </label>
                  {face.immich_url && (
                    <a
                      aria-label={`Open ${face.source_name || "this face"} in Immich`}
                      className="mt-1 flex items-center gap-1 rounded-sm px-2 py-1 text-accent-foreground hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                      href={face.immich_url}
                      rel="noreferrer"
                      target="_blank"
                    >
                      <ExternalLink
                        aria-hidden="true"
                        className="size-3"
                        strokeWidth={1.5}
                      />
                      Immich
                    </a>
                  )}
                </div>
              ))}
            </div>
            <FieldError error={errors.source_face_id} id="avatar-face-error" />
            <Button className="mt-5" disabled={!dirty} type="submit">
              {update.isPending ? "Saving…" : "Save avatar"}
            </Button>
          </fieldset>
          {update.isSuccess && !dirty && (
            <p className="mt-4 text-sm text-muted" role="status">
              Avatar saved.
            </p>
          )}
        </Form>
      )}
      <Failure error={faces.length === 0 ? update.error : null} />
    </section>
  );
}
