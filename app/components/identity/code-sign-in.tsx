import { KeyRound, LogIn, Mail, Send } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import {
  useRequestSignInCode,
  useVerifySignInCode,
} from "../../hooks/queries/identity";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { Field, Form, sectionHeadingClass } from "../people/form-fields";
import { Button } from "../ui/button";

type Step = "email" | "code" | "name" | "requested";

const rememberedEmailKey = "memento-sign-in-email";
const resendDelay = 60_000;

function rememberedEmail() {
  try {
    return window.localStorage.getItem(rememberedEmailKey) ?? "";
  } catch {
    return "";
  }
}

function rememberEmail(email: string) {
  try {
    window.localStorage.setItem(rememberedEmailKey, email);
  } catch {
    // Sign-in still works when browser storage is unavailable.
  }
}

// useResendDelay enables Resend a minute after each code is sent, matching
// the server's one email per address per minute.
function useResendDelay() {
  const [ready, setReady] = useState(false);
  const timerRef = useRef<number>(undefined);
  useEffect(() => () => window.clearTimeout(timerRef.current), []);
  const restart = () => {
    setReady(false);
    window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => setReady(true), resendDelay);
  };
  return [ready, restart] as const;
}

// CodeSignIn signs a Person in with a Sign-in Code emailed to them. The
// notice from an earlier attempt and the alternative (Google, or the
// development form) show only with the first step. An unknown address is
// asked for a name and becomes an Access Request.
export function CodeSignIn({
  alternative,
  claiming,
  notice,
}: {
  alternative: ReactNode;
  claiming: boolean;
  notice: ReactNode;
}) {
  const [step, setStep] = useState<Step>("email");
  const [email, setEmail] = useState(rememberedEmail);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [resent, setResent] = useState(false);
  const [canResend, restartResend] = useResendDelay();
  const requestCode = useRequestSignInCode();
  const verify = useVerifySignInCode();
  useUnsavedChanges(step === "name" && name.trim() !== "");

  const send = (resend: boolean) => {
    rememberEmail(email.trim());
    verify.reset();
    requestCode.mutate(
      { email },
      {
        onSuccess: () => {
          restartResend();
          setResent(resend);
          setCode("");
          setStep("code");
        },
      },
    );
  };
  const check = (value: string, displayName = "") => {
    if (verify.isPending) return;
    requestCode.reset();
    setResent(false);
    verify.mutate(
      { email, code: value, display_name: displayName },
      {
        onSuccess: (result) => {
          if (result.outcome === "name_required") setStep("name");
          if (result.outcome === "requested") setStep("requested");
        },
        // A code that died while the Person typed their name is explained
        // on the code step, where a new one can be sent.
        onError: (error) => {
          if (fieldErrors(error).code) setStep("code");
        },
      },
    );
  };
  const startOver = () => {
    requestCode.reset();
    verify.reset();
    setCode("");
    setName("");
    setStep("email");
  };

  if (step === "email")
    return (
      <div className="min-[761px]:max-w-95">
        {notice}
        <Form
          aria-busy={requestCode.isPending}
          aria-label="Sign in with email"
          error={requestCode.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (!requestCode.isPending) send(false);
          }}
        >
          <fieldset disabled={requestCode.isPending}>
            <Field
              autoCapitalize="none"
              autoComplete="email"
              autoCorrect="off"
              error={fieldErrors(requestCode.error).email}
              label="Email address"
              maxLength={254}
              name="email"
              onChange={(event) => {
                requestCode.reset();
                setEmail(event.target.value);
              }}
              required
              type="email"
              value={email}
            />
            <Button className="w-full" type="submit">
              <Mail aria-hidden="true" className="size-4" strokeWidth={1.5} />
              {requestCode.isPending ? "Sending code…" : "Continue"}
            </Button>
          </fieldset>
        </Form>
        {alternative && (
          <>
            <p className="my-7 flex items-center gap-3 text-xs text-muted before:h-px before:flex-1 before:bg-border after:h-px after:flex-1 after:bg-border">
              or
            </p>
            {alternative}
          </>
        )}
      </div>
    );

  if (step === "code")
    return (
      <div className="min-[761px]:max-w-95">
        <h2 className={sectionHeadingClass}>Check your email</h2>
        <p className="mt-3.5 mb-6.5 text-sm wrap-anywhere text-muted">
          We sent a code to{" "}
          <span className="font-medium text-foreground">{email.trim()}</span>
        </p>
        <Form
          aria-busy={verify.isPending}
          aria-label="Enter your sign-in code"
          error={verify.error ?? requestCode.error}
          onSubmit={(event) => {
            event.preventDefault();
            check(code);
          }}
        >
          <fieldset disabled={verify.isPending}>
            <Field
              autoComplete="one-time-code"
              autoFocus
              error={fieldErrors(verify.error).code}
              inputMode="numeric"
              label="Sign-in code"
              name="code"
              onChange={(event) => {
                // Pasted codes may carry spaces or other text around them.
                const digits = event.target.value
                  .replace(/\D/g, "")
                  .slice(0, 6);
                setCode(digits);
                verify.reset();
                if (digits.length === 6 && digits !== code) check(digits);
              }}
              required
              value={code}
            />
            <Button className="w-full" type="submit">
              {claiming ? (
                <KeyRound
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
              ) : (
                <LogIn
                  aria-hidden="true"
                  className="size-4"
                  strokeWidth={1.5}
                />
              )}
              {verify.isPending
                ? "Checking code…"
                : claiming
                  ? "Claim installation"
                  : "Sign in"}
            </Button>
          </fieldset>
        </Form>
        <p className="mt-6 text-xs/[1.8] text-muted">
          Didn't get it? Check your spam folder. You can send a new code after a
          minute.
        </p>
        {resent && (
          <p className="mt-2 text-xs/[1.8]" role="status">
            We sent a new code.
          </p>
        )}
        <div className="-mx-4 mt-3 flex flex-wrap gap-2">
          <Button
            disabled={!canResend || requestCode.isPending}
            onClick={() => send(true)}
            variant="ghost"
          >
            <Send aria-hidden="true" className="size-4" strokeWidth={1.5} />
            {requestCode.isPending ? "Sending code…" : "Resend code"}
          </Button>
          <Button onClick={startOver} variant="ghost">
            Use a different email
          </Button>
        </div>
      </div>
    );

  if (step === "name")
    return (
      <div className="min-[761px]:max-w-95">
        <h2 className={sectionHeadingClass}>Request access</h2>
        <p className="mt-3.5 mb-6.5 text-sm wrap-anywhere text-muted">
          <span className="font-medium text-foreground">{email.trim()}</span> is
          not part of this Memento yet. Tell your Curator who you are and they
          can let you in.
        </p>
        <Form
          aria-busy={verify.isPending}
          aria-label="Request access"
          error={verify.error}
          onSubmit={(event) => {
            event.preventDefault();
            check(code, name);
          }}
        >
          <fieldset disabled={verify.isPending}>
            <Field
              autoComplete="name"
              autoFocus
              error={fieldErrors(verify.error).display_name}
              label="Your name"
              maxLength={100}
              name="display_name"
              onChange={(event) => {
                verify.reset();
                setName(event.target.value);
              }}
              required
              value={name}
            />
            <Button className="w-full" type="submit">
              <Send aria-hidden="true" className="size-4" strokeWidth={1.5} />
              {verify.isPending ? "Requesting…" : "Request"}
            </Button>
          </fieldset>
        </Form>
        <div className="-mx-4 mt-3">
          <Button onClick={startOver} variant="ghost">
            Use a different email
          </Button>
        </div>
      </div>
    );

  return (
    <div className="min-[761px]:max-w-95">
      <h2 className={sectionHeadingClass}>Request sent</h2>
      <p className="mt-3.5 text-sm text-muted" role="status">
        Your Curator has been asked to review your request.
      </p>
    </div>
  );
}
