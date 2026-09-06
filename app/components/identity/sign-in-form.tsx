import { useEffect, useRef, useState } from "react";

import { useFakeSignIn } from "../../hooks/queries/identity";
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
      className="sign-in-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (!signIn.isPending) signIn.mutate(claims);
      }}
      ref={formRef}
    >
      <h2>Fake development sign-in</h2>
      <p className="form-intro">
        Use the same email to sign back in. A different email represents a
        different person. No password is needed in development. Do not expose
        this installation to the internet.
      </p>
      {signIn.isError && (
        <p className="form-error" role="alert">
          {errorMessage(signIn.error)}
        </p>
      )}
      <fieldset disabled={signIn.isPending}>
        {fields.map((field) => (
          <div className="form-field" key={field.name}>
            <label htmlFor={field.name}>{field.label}</label>
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
              <p className="field-error" id={`${field.name}-error`}>
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
