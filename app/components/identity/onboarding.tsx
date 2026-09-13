import { useId, useState } from "react";

import { useCompleteOnboarding } from "../../hooks/queries/admission";
import { useProfile } from "../../hooks/queries/profile";
import { useViewerAlbums } from "../../hooks/queries/viewer";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type {
  Profile,
  UpdateProfileRequest,
} from "../../types/generated/identity";
import { AlbumImage } from "../albums/album-image";
import { countLabel } from "../albums/moment-labels";
import {
  CheckField,
  Field,
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import { captureRange } from "../viewer/labels";

// Every Person completes this once, whether they arrived through an Invitation
// or signed in directly. Completion also fixes their notification baseline, so
// the Albums shown here are exactly what will not be announced later.
export function WelcomePage() {
  const profile = useProfile();
  return (
    <div className="mx-auto max-w-200">
      <PageTitle title="Welcome" />
      <h1 className={headingClass}>Welcome to memento</h1>
      <p className="mt-5 max-w-[590px] text-muted">
        Check your name and how you want to hear about new photos. You can
        change these later in your profile.
      </p>
      {profile.isPending && (
        <p className="mt-6" role="status">
          Loading your details…
        </p>
      )}
      {profile.isError && (
        <div className="mt-6">
          <ReadFailure
            error={profile.error}
            pending={profile.isFetching}
            retry={profile.refetch}
          />
        </div>
      )}
      {profile.data && <OnboardingForm profile={profile.data} />}
    </div>
  );
}

function OnboardingForm({ profile }: { profile: Profile }) {
  const initial: UpdateProfileRequest = {
    display_name: profile.person.display_name,
    update_email: profile.person.update_email,
    email_updates: profile.person.email_updates,
  };
  const [draft, setDraft] = useState<UpdateProfileRequest | null>(null);
  const values = draft ?? initial;
  const complete = useCompleteOnboarding();
  const dirty =
    draft !== null && JSON.stringify(draft) !== JSON.stringify(initial);
  useUnsavedChanges((dirty || complete.isPending) && !complete.isSuccess);
  const errors = fieldErrors(complete.error);
  const emailId = useId();
  const emails = [
    ...new Set(profile.identities.map((identity) => identity.email)),
  ];
  return (
    <div className="mt-9 grid gap-x-12 gap-y-10 min-[1001px]:grid-cols-[minmax(0,380px)_minmax(0,1fr)]">
      <Form
        aria-busy={complete.isPending}
        aria-label="Complete onboarding"
        className="max-w-110"
        error={complete.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (!complete.isPending) complete.mutate(values);
        }}
      >
        <fieldset disabled={complete.isPending}>
          <Field
            error={errors.display_name}
            label="Your name"
            maxLength={100}
            name="display_name"
            onChange={(event) => {
              complete.reset();
              setDraft({ ...values, display_name: event.target.value });
            }}
            required
            value={values.display_name}
          />
          <div className="mb-5">
            <label className="mb-2 block text-xs font-medium" htmlFor={emailId}>
              Email for updates
            </label>
            <Combobox
              aria-describedby={
                errors.update_email ? `${emailId}-error` : undefined
              }
              aria-invalid={!!errors.update_email}
              disabled={complete.isPending}
              id={emailId}
              onChange={(email) => {
                complete.reset();
                setDraft({
                  ...values,
                  update_email: email === "none" ? "" : email,
                });
              }}
              options={[
                { value: "none", label: "No email selected" },
                ...emails.map((email) => ({ value: email, label: email })),
              ]}
              value={values.update_email || "none"}
            />
            <FieldError error={errors.update_email} id={`${emailId}-error`} />
          </div>
          <CheckField
            checked={values.email_updates}
            error={errors.email_updates}
            name="email_updates"
            onChange={(event) => {
              complete.reset();
              setDraft({ ...values, email_updates: event.target.checked });
            }}
          >
            Email me when there are updates
          </CheckField>
          <Button type="submit">
            {complete.isPending ? "Finishing…" : "Continue to memento"}
          </Button>
        </fieldset>
      </Form>
      <AvailableAlbums curator={profile.person.is_curator} />
    </div>
  );
}

function AvailableAlbums({ curator }: { curator: boolean }) {
  const albums = useViewerAlbums();
  return (
    <section aria-labelledby="available-albums" className="min-w-0">
      <h2 className={sectionHeadingClass} id="available-albums">
        {curator ? "Albums in this installation" : "Albums shared with you"}
      </h2>
      {albums.isPending && (
        <p className="mt-4 text-muted" role="status">
          Loading albums…
        </p>
      )}
      {albums.isError && (
        <div className="mt-4">
          <ReadFailure
            error={albums.error}
            pending={albums.isFetching}
            retry={albums.refetch}
          />
        </div>
      )}
      {albums.data?.length === 0 && (
        <p className="mt-4 max-w-120 text-muted">
          {curator
            ? "Nothing is imported yet. You'll bring albums in from Immich after this step."
            : "Nothing is shared with you yet. Your Curator will choose what to share, and you'll hear about it here."}
        </p>
      )}
      {albums.data && albums.data.length > 0 && (
        <ul className="mt-5 grid grid-cols-2 gap-x-5 gap-y-6 min-[601px]:grid-cols-3">
          {albums.data.map((album) => (
            <li className="min-w-0" key={album.id}>
              <AlbumImage
                alt={album.title}
                className="aspect-square w-full object-cover"
                fallback="No cover"
                src={album.cover_url}
              />
              <span className="mt-3 block font-heading text-lg wrap-anywhere">
                {album.title}
              </span>
              <span className="block text-xs text-muted">
                {countLabel(album.photo_count, "photo", "photos")},{" "}
                {countLabel(album.video_count, "video", "videos")}
              </span>
              <span className="block text-xs text-muted">
                {captureRange(album)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
