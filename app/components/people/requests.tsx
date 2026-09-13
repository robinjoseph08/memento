import { useId, useState } from "react";
import { Link, useNavigate } from "react-router-dom";

import {
  useAccessRequests,
  useApproveAccessRequest,
  useDenyAccessRequest,
  useReconsiderAccessRequest,
} from "../../hooks/queries/admission";
import { usePeople } from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { errorMessage, fieldErrors } from "../../lib/http";
import { formatDate } from "../../lib/utils";
import type { AccessRequest } from "../../types/generated/identity";
import { ConfirmAction } from "../forms/confirm-action";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import { Combobox } from "../ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import {
  Field,
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "./form-fields";

const providerLabels: Record<string, string> = {
  google: "Google",
  fake: "Fake sign-in",
};

export function RequestsPage() {
  const requests = useAccessRequests();
  const pending = requests.data?.filter((r) => r.status === "pending") ?? [];
  const decided = requests.data?.filter((r) => r.status !== "pending") ?? [];
  return (
    <>
      <PageTitle title="Requests" />
      <h1 className={headingClass}>Access requests</h1>
      <p className="mt-5 max-w-[640px] text-muted">
        People who signed in without access, or asked to see an album they
        cannot open. Approving records your decision and lets the email sign in
        as a person; what they can see is still decided in each album.
      </p>
      {requests.isPending && (
        <p className="mt-9" role="status">
          Loading requests…
        </p>
      )}
      {requests.isError && (
        <div className="mt-9">
          <ReadFailure
            error={requests.error}
            pending={requests.isFetching}
            retry={requests.refetch}
          />
        </div>
      )}
      {requests.data && (
        <>
          <section aria-labelledby="pending-requests" className="mt-9">
            <h2 className={sectionHeadingClass} id="pending-requests">
              Waiting for a decision ({pending.length})
            </h2>
            {pending.length === 0 ? (
              <p className="mt-4 text-muted">
                Nothing is waiting. New requests appear here when someone signs
                in without access or asks for an album.
              </p>
            ) : (
              <ul className="mt-5 divide-y divide-border border-y border-border">
                {pending.map((request) => (
                  <RequestRow key={request.id} request={request} />
                ))}
              </ul>
            )}
          </section>
          {decided.length > 0 && (
            <section aria-labelledby="decided-requests" className="mt-12">
              <h2 className={sectionHeadingClass} id="decided-requests">
                Decided ({decided.length})
              </h2>
              <ul className="mt-5 divide-y divide-border border-y border-border">
                {decided.map((request) => (
                  <RequestRow key={request.id} request={request} />
                ))}
              </ul>
            </section>
          )}
        </>
      )}
    </>
  );
}

function RequestRow({ request }: { request: AccessRequest }) {
  const deny = useDenyAccessRequest();
  const reconsider = useReconsiderAccessRequest();
  const approve = useApproveAccessRequest();
  const navigate = useNavigate();
  const [approving, setApproving] = useState(false);
  const albumRequest = !!request.person_id;
  const count = request.sign_in_count;
  const history = albumRequest
    ? `${count === 1 ? "1 request" : `${count} requests`}`
    : `${count === 1 ? "1 sign-in" : `${count} sign-ins`}`;
  const error = deny.error ?? reconsider.error ?? approve.error;
  return (
    <li className="py-5" data-request-status={request.status}>
      <div className="flex flex-wrap items-start justify-between gap-x-8 gap-y-4">
        <div className="max-w-[640px] min-w-0">
          <p className="wrap-anywhere">
            <span className="font-medium">
              {albumRequest ? request.person_name : request.display_name}
            </span>{" "}
            <span className="text-muted">
              {request.email} ·{" "}
              {providerLabels[request.provider] ?? request.provider}
              {request.email_verified ? " · verified email" : " · unverified"}
            </span>
          </p>
          {albumRequest ? (
            <p className="mt-2 text-sm text-muted">
              Existing person{" "}
              <Link
                className="text-accent-foreground underline underline-offset-4"
                to={`/curator/people/${request.person_id}`}
              >
                {request.person_name}
              </Link>{" "}
              asked for{" "}
              {request.album_id ? (
                <Link
                  className="text-accent-foreground underline underline-offset-4"
                  to={`/curator/albums/${request.album_id}`}
                >
                  {request.album_title || "an album"}
                </Link>
              ) : (
                "an album that no longer exists"
              )}
              .
            </p>
          ) : (
            <p className="mt-2 text-sm text-muted">
              Signed in with an account Memento does not know. The name comes
              from the sign-in provider and is not proof of who this is.
            </p>
          )}
          <p className="mt-2 text-xs text-muted">
            First {formatDate(request.created_at)} · Last{" "}
            {formatDate(request.updated_at)} · {history}
          </p>
          {request.status === "denied" && (
            <p className="mt-2 text-xs text-muted">
              Denied by {request.resolved_by || "a Curator"}
              {request.resolved_at ? ` ${formatDate(request.resolved_at)}` : ""}
              .
            </p>
          )}
          {request.status === "approved" && (
            <p className="mt-2 text-xs text-muted">
              Approved by {request.resolved_by || "a Curator"}
              {request.resolved_at ? ` ${formatDate(request.resolved_at)}` : ""}
              {request.person_id && (
                <>
                  {" · "}
                  <Link
                    className="text-accent-foreground underline underline-offset-4"
                    to={`/curator/people/${request.person_id}`}
                  >
                    Open {request.person_name || "person"}
                  </Link>
                </>
              )}
            </p>
          )}
          {error && (
            <p className="mt-3 text-sm text-destructive" role="alert">
              {errorMessage(error)}
            </p>
          )}
        </div>
        {request.status !== "approved" && (
          <div className="flex flex-wrap gap-2">
            {albumRequest ? (
              <ConfirmAction
                description="This records your decision. It does not change what they can see; open the album to grant access."
                error={approve.error}
                label={`Approve request from ${request.person_name}`}
                onConfirm={() =>
                  approve
                    .mutateAsync({ id: request.id })
                    .then(() =>
                      request.album_id
                        ? navigate(`/curator/albums/${request.album_id}`)
                        : undefined,
                    )
                }
                pending={approve.isPending}
                triggerLabel="Approve"
              />
            ) : (
              <Button
                aria-haspopup="dialog"
                aria-label={`Approve request from ${request.email}`}
                onClick={() => setApproving(true)}
                variant="outline"
              >
                Approve…
              </Button>
            )}
            {request.status === "pending" ? (
              <Button
                aria-label={`Deny request from ${request.email}`}
                disabled={deny.isPending}
                onClick={() => deny.mutate({ id: request.id })}
                variant="ghost"
              >
                {deny.isPending ? "Denying…" : "Deny"}
              </Button>
            ) : (
              <Button
                aria-label={`Reconsider request from ${request.email}`}
                disabled={reconsider.isPending}
                onClick={() => reconsider.mutate({ id: request.id })}
                variant="ghost"
              >
                {reconsider.isPending ? "Reopening…" : "Reconsider"}
              </Button>
            )}
          </div>
        )}
      </div>
      {!albumRequest && (
        <ApproveDialog
          onOpenChange={setApproving}
          open={approving}
          request={request}
        />
      )}
    </li>
  );
}

// Approval never grants Album access. It links or creates the Person and
// approves the exact verified email; the Person page then offers an Invitation.
function ApproveDialog({
  request,
  open,
  onOpenChange,
}: {
  request: AccessRequest;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [mode, setMode] = useState<"create" | "link">("create");
  const [name, setName] = useState(request.display_name);
  const [personID, setPersonID] = useState("");
  const people = usePeople("");
  const approve = useApproveAccessRequest();
  const navigate = useNavigate();
  const personFieldId = useId();
  const errors = fieldErrors(approve.error);
  useUnsavedChanges(open && approve.isPending);
  const options = (people.data ?? [])
    .filter((person) => !person.deactivated_at)
    .map((person) => ({
      value: person.id,
      label: person.display_name,
      description: person.is_curator ? "Curator" : undefined,
    }));
  return (
    <Dialog
      onOpenChange={(next) => {
        if (approve.isPending) return;
        approve.reset();
        onOpenChange(next);
      }}
      open={open}
    >
      <DialogContent>
        <DialogTitle className="pr-8 wrap-anywhere">
          Approve {request.email}
        </DialogTitle>
        <DialogDescription className="mt-3 mb-5 text-sm text-muted">
          Choose who this is. Memento approves the exact email for that person
          so their next sign-in links the account. Album access is a separate
          step, and you can send an invitation from the person's page.
        </DialogDescription>
        <Form
          aria-busy={approve.isPending}
          aria-label="Approve access request"
          error={approve.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (approve.isPending) return;
            approve.mutate(
              {
                id: request.id,
                body:
                  mode === "create"
                    ? { person_id: "", display_name: name }
                    : { person_id: personID, display_name: "" },
              },
              {
                onSuccess: (approved) => {
                  onOpenChange(false);
                  if (approved.person_id)
                    void navigate(`/curator/people/${approved.person_id}`);
                },
              },
            );
          }}
        >
          <fieldset disabled={approve.isPending}>
            <legend className="sr-only">Who is this?</legend>
            <div className="mb-5 flex flex-col gap-3 text-sm">
              <label className="flex cursor-pointer items-center gap-3">
                <input
                  checked={mode === "create"}
                  className="size-4 cursor-pointer accent-primary"
                  name="approve-mode"
                  onChange={() => setMode("create")}
                  type="radio"
                  value="create"
                />
                Create a new person
              </label>
              <label className="flex cursor-pointer items-center gap-3">
                <input
                  checked={mode === "link"}
                  className="size-4 cursor-pointer accent-primary"
                  name="approve-mode"
                  onChange={() => setMode("link")}
                  type="radio"
                  value="link"
                />
                Link to an existing person
              </label>
            </div>
            {mode === "create" ? (
              <Field
                error={errors.display_name}
                label="Display name"
                maxLength={100}
                name="display_name"
                onChange={(event) => {
                  approve.reset();
                  setName(event.target.value);
                }}
                required
                value={name}
              />
            ) : (
              <div className="mb-5">
                <label
                  className="mb-2 block text-xs font-medium"
                  htmlFor={personFieldId}
                >
                  Person
                </label>
                <Combobox
                  aria-describedby={
                    errors.person_id ? `${personFieldId}-error` : undefined
                  }
                  aria-invalid={!!errors.person_id}
                  emptyText={
                    people.isPending ? "Loading people…" : "No matching people."
                  }
                  id={personFieldId}
                  onChange={(value) => {
                    approve.reset();
                    setPersonID(value);
                  }}
                  options={options}
                  placeholder="Choose a person"
                  searchPlaceholder="Search people…"
                  value={personID}
                />
                <FieldError
                  error={errors.person_id}
                  id={`${personFieldId}-error`}
                />
              </div>
            )}
            <Button type="submit">
              {approve.isPending ? "Approving…" : "Approve and open person"}
            </Button>
            <Button
              className="ml-3"
              onClick={() => onOpenChange(false)}
              variant="ghost"
            >
              Cancel
            </Button>
          </fieldset>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
