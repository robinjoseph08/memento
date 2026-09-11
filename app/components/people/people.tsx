import { useState } from "react";
import { Link, Navigate, useParams, useSearchParams } from "react-router-dom";

import {
  useCreatePerson,
  usePeople,
  usePerson,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import { initials } from "../../lib/initials";
import { ConfirmDialog } from "../forms/confirm-dialog";
import { SearchForm } from "../forms/search-form";
import { BackLink } from "../shell/back-link";
import { PageTitle } from "../shell/page-title";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "../ui/dialog";
import {
  Field,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "./form-fields";
import { PersonDetails } from "./person-details";

export function PeoplePage() {
  const [search, setSearch] = useSearchParams();
  const query = usePeople(search.get("q") ?? "");
  const [open, setOpen] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [name, setName] = useState("");
  const create = useCreatePerson();
  useUnsavedChanges((!!name || create.isPending) && !create.isSuccess);
  function close() {
    setDiscardOpen(false);
    setOpen(false);
    setName("");
    create.reset();
  }
  function changeOpen(next: boolean) {
    if (next) setOpen(true);
    else if (create.isPending) return;
    else if (name) setDiscardOpen(true);
    else close();
  }
  if (create.isSuccess)
    return <Navigate to={`/curator/people/${create.data.id}`} />;
  return (
    <>
      <PageTitle title="People" />
      <div className="mb-9 flex flex-wrap items-center justify-between gap-5">
        <h1 className={headingClass}>People</h1>
        <Dialog onOpenChange={changeOpen} open={open}>
          <DialogTrigger asChild>
            <Button>Add person</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogTitle>Add person</DialogTitle>
            <DialogDescription className="mt-3 mb-6 text-sm text-muted">
              Create a person, then approve an email address when you're ready
              to give them access.
            </DialogDescription>
            <Form
              aria-busy={create.isPending}
              aria-label="Create person"
              error={create.error}
              onSubmit={(event) => {
                event.preventDefault();
                if (!create.isPending) create.mutate({ display_name: name });
              }}
            >
              <fieldset disabled={create.isPending}>
                <Field
                  error={fieldErrors(create.error).display_name}
                  label="Display name"
                  maxLength={100}
                  name="display_name"
                  onChange={(event) => setName(event.target.value)}
                  required
                  value={name}
                />
                <Button type="submit">
                  {create.isPending ? "Creating…" : "Create person"}
                </Button>
              </fieldset>
            </Form>
          </DialogContent>
        </Dialog>
        <ConfirmDialog
          confirmLabel="Discard"
          description="The name you entered will not be saved."
          onConfirm={close}
          onOpenChange={setDiscardOpen}
          open={discardOpen}
          title="Discard this new person?"
        />
      </div>
      <SearchForm
        className="mb-7"
        label="Search people"
        onSearch={(value) => setSearch(value ? { q: value } : {})}
        value={search.get("q") ?? ""}
      />
      {query.isPending && <p role="status">Loading people…</p>}
      {query.isError && (
        <ReadFailure
          error={query.error}
          pending={query.isFetching}
          retry={query.refetch}
        />
      )}
      {query.data &&
        (query.data.length ? (
          <ul className="divide-y divide-border border-y border-border">
            {query.data.map((person) => (
              <li key={person.id}>
                <Link
                  className="flex cursor-pointer items-center gap-3 px-3 py-3 hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring"
                  to={`/curator/people/${person.id}`}
                >
                  <Avatar aria-hidden="true" className="size-9">
                    {person.avatar_url && (
                      <AvatarImage alt="" src={person.avatar_url} />
                    )}
                    <AvatarFallback className="text-xs">
                      {initials(person.display_name)}
                    </AvatarFallback>
                  </Avatar>
                  <span className="min-w-0">
                    <span className="block wrap-anywhere">
                      {person.display_name}
                    </span>
                    <span className="text-xs text-muted">
                      {person.is_curator ? "Curator" : "Member"}
                      {person.deactivated_at ? ", deactivated" : ""}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <section className="border-t border-border py-8">
            <h2 className={sectionHeadingClass}>
              {search.get("q") ? "No matching people" : "No people yet"}
            </h2>
            <p className="mt-3 text-muted">
              {search.get("q")
                ? "Try another name."
                : "Add friends and family here. Creating a person does not give them sign-in access."}
            </p>
          </section>
        ))}
    </>
  );
}

export function PersonPage() {
  const { id = "" } = useParams();
  const query = usePerson(id);
  return (
    <div className="mx-auto max-w-280">
      <PageTitle title={query.data?.person.display_name ?? "Person"} />
      <BackLink to="/curator/people">All people</BackLink>
      {query.isPending && <p role="status">Loading person…</p>}
      {query.isError && (
        <ReadFailure
          error={query.error}
          pending={query.isFetching}
          retry={query.refetch}
        />
      )}
      {query.data && <PersonDetails detail={query.data} key={id} />}
    </div>
  );
}
