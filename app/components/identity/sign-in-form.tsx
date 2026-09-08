import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { useFakeSignIn, useIdentityStatus } from "../../hooks/queries/identity";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { focusFirstInvalid } from "../../lib/forms";
import { errorMessage, HTTPError } from "../../lib/http";
import type { SignInRequest } from "../../types/generated/identity";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

const initialClaims: SignInRequest = {
  email: "curator@example.test",
  display_name: "Local Curator",
};
const fields = [
  { name: "email", label: "Email", type: "email", maxLength: 254 },
  { name: "display_name", label: "Display name", type: "text", maxLength: 100 },
] as const;

export function SignInForm({ claiming = false }: { claiming?: boolean }) {
  const { data } = useIdentityStatus();
  const [search] = useSearchParams();
  const error = search.get("error");
  return (
    <div>
      {error && (
        <p className="mb-6 max-w-110 text-sm text-destructive" role="alert">
          {error === "no_access" || error === "access_denied"
            ? "This Google account does not have access. Ask your Curator to approve your exact Google email address."
            : error === "provider_unavailable"
              ? "Google sign-in is unavailable. Try again later."
              : error === "unverified_identity"
                ? "Use a Google account with a verified email address."
                : "Sign-in could not be completed. Start again with Google."}
        </p>
      )}
      {data?.auth_mode === "google" ? (
        <Button asChild>
          <a href="/api/identity/google/start">Continue with Google</a>
        </Button>
      ) : data?.auth_mode === "fake" ? (
        <FakeSignInForm claiming={claiming} />
      ) : (
        <p role="alert">Sign-in is not configured. Contact your Curator.</p>
      )}
    </div>
  );
}

function FakeSignInForm({ claiming }: { claiming: boolean }) {
  const [claims, setClaims] = useState(initialClaims);
  const formRef = useRef<HTMLFormElement>(null);
  const signIn = useFakeSignIn();
  const dirty = fields.some(({ name }) => claims[name] !== initialClaims[name]);
  useUnsavedChanges((dirty || signIn.isPending) && !signIn.isSuccess);
  const errors = signIn.error instanceof HTTPError ? signIn.error.fields : {};
  useEffect(() => {
    if (signIn.isError) focusFirstInvalid(formRef.current);
  }, [signIn.isError, signIn.error]);

  return (
    <form
      aria-busy={signIn.isPending}
      aria-label="Fake development sign-in"
      className="min-[761px]:max-w-95"
      onSubmit={(event) => {
        event.preventDefault();
        if (!signIn.isPending) signIn.mutate(claims);
      }}
      ref={formRef}
    >
      <h2 className="font-heading text-[27px]/[1.2] font-normal tracking-[-0.35px]">
        Fake development sign-in
      </h2>
      <p className="mt-3.5 mb-6.5 text-xs/[1.8] text-muted">
        Use an approved email to sign in. A Curator can link more than one email
        to the same person. No password is needed in development. Do not expose
        this installation to the internet.
      </p>
      {signIn.isError && (
        <p className="mb-5.5 text-destructive" role="alert">
          {errorMessage(signIn.error)}
        </p>
      )}
      <fieldset disabled={signIn.isPending}>
        {fields.map((field) => (
          <div className="mb-5" key={field.name}>
            <label
              className="mb-1.75 block text-xs/[1.8] font-medium"
              htmlFor={field.name}
            >
              {field.label}
            </label>
            <Input
              aria-describedby={
                errors[field.name] ? `${field.name}-error` : undefined
              }
              aria-invalid={!!errors[field.name]}
              autoCapitalize="none"
              id={field.name}
              maxLength={field.maxLength}
              name={field.name}
              onChange={(event) =>
                setClaims({ ...claims, [field.name]: event.target.value })
              }
              required
              type={field.type}
              value={claims[field.name]}
            />
            {errors[field.name] && (
              <p
                className="mt-1.75 text-xs/[1.8] text-destructive"
                id={`${field.name}-error`}
              >
                {errors[field.name]}
              </p>
            )}
          </div>
        ))}
        <Button className="mt-2 w-full" type="submit">
          {signIn.isPending
            ? "Signing in…"
            : claiming
              ? "Claim installation"
              : "Sign in"}
        </Button>
      </fieldset>
    </form>
  );
}
