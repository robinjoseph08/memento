import { useEffect, useRef, useState } from "react";
import { Link, Navigate, useParams, useSearchParams } from "react-router-dom";

import {
  useCreatePerson,
  usePeople,
  usePerson,
} from "../../hooks/queries/people";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
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
  const searchInputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (searchInputRef.current)
      searchInputRef.current.value = search.get("q") ?? "";
  }, [search]);
  const query = usePeople(search.get("q") ?? "");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const create = useCreatePerson();
  useUnsavedChanges((!!name || create.isPending) && !create.isSuccess);
  function changeOpen(next: boolean) {
    if (
      !next &&
      (create.isPending ||
        (name && !window.confirm("Discard this new person?")))
    )
      return;
    setOpen(next);
    if (!next) {
      setName("");
      create.reset();
    }
  }
  if (create.isSuccess)
    return <Navigate to={`/curator/people/${create.data.id}`} />;
  return (
    <>
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
      </div>
      <form
        className="mb-7 grid max-w-xl grid-cols-[minmax(0,1fr)_auto] items-end gap-2 [&>div]:mb-0"
        onSubmit={(event) => {
          event.preventDefault();
          const values = new FormData(event.currentTarget);
          setSearch(values.get("q") ? { q: String(values.get("q")) } : {});
          searchInputRef.current?.focus();
        }}
        role="search"
      >
        <Field
          defaultValue={search.get("q") ?? ""}
          label="Search people"
          name="q"
          ref={searchInputRef}
          type="search"
        />
        <Button type="submit" variant="outline">
          Search
        </Button>
      </form>
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
                  <span
                    aria-hidden="true"
                    className="flex size-9 shrink-0 items-center justify-center rounded-full bg-surface text-xs"
                  >
                    {person.display_name
                      .split(/\s+/)
                      .slice(0, 2)
                      .map((part) => Array.from(part)[0])
                      .join("")
                      .toLocaleUpperCase()}
                  </span>
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
      <Link
        className="mb-6 inline-block text-sm text-accent-foreground underline underline-offset-4"
        to="/curator/people"
      >
        All people
      </Link>
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
