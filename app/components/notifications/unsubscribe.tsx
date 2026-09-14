import { Link, useSearchParams } from "react-router-dom";

import {
  useUnsubscribe,
  useUnsubscribeStatus,
} from "../../hooks/queries/notifications";
import { HTTPError } from "../../lib/http";
import { pageClassName } from "../pages/layouts";
import { Form, headingClass, ReadFailure } from "../people/form-fields";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";

// Opened from an email without signing in. Loading the page changes nothing;
// only the confirmation below switches update email off, so a mail scanner
// following the link cannot unsubscribe anyone. The token is a query value
// so server access logs, which record paths, never carry it.
export function UnsubscribePage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const status = useUnsubscribeStatus(token);
  const unsubscribe = useUnsubscribe(token);
  const missing =
    status.isError &&
    status.error instanceof HTTPError &&
    status.error.status === 404;
  return (
    <main className={pageClassName}>
      <PageTitle title="Unsubscribe" />
      <div className="max-w-160">
        {status.isPending && (
          <p className="text-muted" role="status">
            Checking this link…
          </p>
        )}
        {missing && (
          <>
            <h1 className={headingClass}>This link is no longer valid</h1>
            <p className="mt-5 text-muted">
              Sign in to Memento to change how you hear about new photos and
              videos from your profile.
            </p>
            <Button asChild className="mt-6" variant="outline">
              <Link to="/sign-in">Sign in</Link>
            </Button>
          </>
        )}
        {status.isError && !missing && (
          <ReadFailure
            error={status.error}
            pending={status.isFetching}
            retry={status.refetch}
          />
        )}
        {status.data && !status.data.subscribed && (
          <>
            <h1 className={headingClass}>Update emails are off</h1>
            <p className="mt-5 text-muted" role="status">
              {status.data.email || "Your email"} will no longer get emails
              about new photos and videos. You can still see every update when
              you sign in to Memento, and invitations and account emails still
              arrive.
            </p>
            <Button asChild className="mt-6" variant="outline">
              <Link to="/sign-in">Sign in to Memento</Link>
            </Button>
          </>
        )}
        {status.data?.subscribed && (
          <>
            <h1 className={headingClass}>Stop update emails?</h1>
            <p className="mt-5 text-muted">
              Hi {status.data.display_name}. Confirm below to stop emails to{" "}
              {status.data.email} about new photos and videos. Nothing changes
              until you confirm. You can turn them back on any time from your
              profile.
            </p>
            <Form
              aria-busy={unsubscribe.isPending}
              aria-label="Stop update emails"
              className="mt-6"
              error={unsubscribe.error}
              onSubmit={(event) => {
                event.preventDefault();
                if (!unsubscribe.isPending) unsubscribe.mutate();
              }}
            >
              <Button disabled={unsubscribe.isPending} type="submit">
                {unsubscribe.isPending ? "Stopping…" : "Stop update emails"}
              </Button>
            </Form>
          </>
        )}
      </div>
    </main>
  );
}
