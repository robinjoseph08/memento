import { KeyRound, LogIn } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { useFakeSignIn, useIdentityStatus } from "../../hooks/queries/identity";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { focusFirstInvalid } from "../../lib/forms";
import { errorMessage, HTTPError } from "../../lib/http";
import type { SignInRequest } from "../../types/generated/identity";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { CodeSignIn } from "./code-sign-in";

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
  // The Mobile App's return link goes with the Person to Google and back.
  const returnTo = search.get("return_to");
  const googleStart = returnTo
    ? `/api/identity/google/start?return_to=${encodeURIComponent(returnTo)}`
    : "/api/identity/google/start";
  const methods = data?.sign_in_methods ?? [];
  const alternative = (methods.includes("google") ||
    methods.includes("fake")) && (
    <div className="space-y-6">
      {methods.includes("google") && (
        <Button asChild className="w-full">
          <a href={googleStart}>
            <LogIn aria-hidden="true" className="size-4" strokeWidth={1.5} />
            Continue with Google
          </a>
        </Button>
      )}
      {methods.includes("fake") && <FakeSignInForm claiming={claiming} />}
    </div>
  );
  const notice = error && (
    <p className="mb-6 max-w-110 text-sm text-destructive" role="alert">
      {error === "no_access" || error === "access_denied"
        ? "This email address does not have access. Ask your Curator to approve it."
        : error === "access_requested"
          ? "This email address does not have access yet. Your Curator has been asked to review your request, so there is nothing more to do right now."
          : error === "provider_unavailable"
            ? "Google sign-in is unavailable. Try again later."
            : error === "unverified_email"
              ? "This email address is not verified. Sign in with a verified email address."
              : "Sign-in could not be completed. Try again."}
    </p>
  );
  if (methods.includes("code"))
    return (
      <CodeSignIn
        alternative={alternative}
        claiming={claiming}
        notice={notice}
      />
    );
  return (
    <div>
      {notice}
      {alternative || (
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
          {claiming ? (
            <KeyRound aria-hidden="true" className="size-4" strokeWidth={1.5} />
          ) : (
            <LogIn aria-hidden="true" className="size-4" strokeWidth={1.5} />
          )}
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
