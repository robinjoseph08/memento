import { Blend, Pencil, Plus, Trash2, UsersRound } from "lucide-react";
import { useState, type ReactNode } from "react";

import {
  useCircleOffers,
  useCircles,
  useCreateCircle,
  useDeleteCircle,
  useRenameCircle,
  useSetCircleMembers,
} from "../../hooks/queries/circles";
import { usePeople } from "../../hooks/queries/people";
import { useReturnFocus } from "../../hooks/use-return-focus";
import { useUnsavedChanges } from "../../hooks/use-unsaved-changes";
import { fieldErrors } from "../../lib/http";
import type { Circle } from "../../types/generated/identity";
import type { OfferedAlbum } from "../../types/generated/publishing";
import { PersonAvatar } from "../albums/person-avatar";
import { ConfirmAction } from "../forms/confirm-action";
import { ConfirmDialog } from "../forms/confirm-dialog";
import {
  Field,
  FieldError,
  Form,
  headingClass,
  ReadFailure,
  sectionHeadingClass,
} from "../people/form-fields";
import { EmptyState } from "../shell/empty-state";
import { PageTitle } from "../shell/page-title";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";

export function CirclesPage() {
  const query = useCircles();
  const offers = useCircleOffers();
  const create = useCreateCircle();
  const [creating, setCreating] = useState(false);
  return (
    <div className="mx-auto max-w-280">
      <PageTitle title="Circles" />
      <div className="mb-3 flex flex-wrap items-center justify-between gap-5">
        <h1 className={headingClass}>Circles</h1>
        <Button onClick={() => setCreating(true)}>
          <Plus aria-hidden="true" className="size-4" strokeWidth={1.5} />
          New circle
        </Button>
      </div>
      <p className="mb-9 max-w-150 text-sm text-muted">
        Group people who share a part of your life, such as extended family or
        college friends. Someone can be in several Circles. Only Curators see
        Circle names.
      </p>
      <NameDialog
        description="Name the group the way you think of it. Only Curators see it."
        mutation={create}
        onOpenChange={setCreating}
        open={creating}
        submitLabel="Create circle"
        title="New circle"
      />
      {query.isPending && <p role="status">Loading circles…</p>}
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
            {query.data.map((circle) => (
              <CircleRow
                circle={circle}
                key={circle.id}
                offered={
                  offers.data?.find((offer) => offer.circle_id === circle.id)
                    ?.albums ?? []
                }
              />
            ))}
          </ul>
        ) : (
          <EmptyState icon={Blend} title="No circles yet">
            Create a Circle, then tick who belongs in it.
          </EmptyState>
        ))}
    </div>
  );
}

// Albums shown by title in the delete warning before the rest collapse into
// a count.
const namedAlbums = 3;

// What deleting a Circle takes away. Its Offers go with it, so the warning
// names the Albums its members could browse through it.
function deleteDescription(offered: OfferedAlbum[]) {
  const kept = "The people in it stay in Memento.";
  if (offered.length === 0)
    return `${kept} They're only taken out of this Circle.`;
  const titles = offered
    .slice(0, namedAlbums)
    .map((album) => `“${album.title}”`);
  const rest = offered.length - titles.length;
  if (rest > 0)
    titles.push(rest === 1 ? "1 more album" : `${rest} more albums`);
  const list =
    titles.length === 1
      ? titles[0]
      : `${titles.slice(0, -1).join(", ")}${titles.length > 2 ? "," : ""} and ${titles.at(-1)}`;
  return `Deleting it withdraws its Offers of ${list}, so its members can't browse them anymore unless they have access another way. ${kept}`;
}

function CircleRow({
  circle,
  offered,
}: {
  circle: Circle;
  offered: OfferedAlbum[];
}) {
  const [editing, setEditing] = useState<"members" | "name" | null>(null);
  const remove = useDeleteCircle(circle.id);
  const rename = useRenameCircle(circle.id);
  const headingID = `circle-${circle.id}`;
  return (
    <li aria-labelledby={headingID} className="py-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h2 className={`${sectionHeadingClass} wrap-anywhere`} id={headingID}>
            {circle.name}
          </h2>
          {circle.members.length > 0 && (
            <p className="mt-1 text-xs text-muted">
              {circle.members.length === 1
                ? "1 person"
                : `${circle.members.length} people`}
            </p>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            aria-label={`Edit members of ${circle.name}`}
            onClick={() => setEditing("members")}
            size="sm"
            variant="outline"
          >
            <UsersRound
              aria-hidden="true"
              className="size-4"
              strokeWidth={1.5}
            />
            Members
          </Button>
          <Button
            aria-label={`Rename ${circle.name}`}
            onClick={() => setEditing("name")}
            size="sm"
            variant="outline"
          >
            <Pencil aria-hidden="true" className="size-4" strokeWidth={1.5} />
            Rename
          </Button>
          <ConfirmAction
            compact
            confirmLabel="Delete"
            description={deleteDescription(offered)}
            destructive="deletes"
            error={remove.error}
            icon={Trash2}
            label={`Delete ${circle.name}`}
            onConfirm={() => remove.mutateAsync()}
            pending={remove.isPending}
            triggerLabel="Delete"
          />
        </div>
      </div>
      {circle.members.length > 0 ? (
        <ul
          aria-label={`Members of ${circle.name}`}
          className="mt-4 flex flex-wrap gap-2"
        >
          {circle.members.map((person) => (
            <li
              className="inline-flex items-center gap-2 rounded-full bg-surface py-1 pr-3 pl-1 text-sm"
              key={person.id}
            >
              <span aria-hidden="true">
                <PersonAvatar className="size-6" person={person} />
              </span>
              {person.display_name}
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-4 text-sm text-muted">Nobody is in this Circle yet.</p>
      )}
      <NameDialog
        description="Only Curators see Circle names."
        initial={circle.name}
        mutation={rename}
        onOpenChange={(open) => setEditing(open ? "name" : null)}
        open={editing === "name"}
        submitLabel="Save name"
        title={`Rename ${circle.name}`}
      />
      <MembersDialog
        circle={circle}
        onOpenChange={(open) => setEditing(open ? "members" : null)}
        open={editing === "members"}
      />
    </li>
  );
}

// A dialog holding a draft. Closing it with unsaved edits asks first, and it
// stays open while a save is in flight.
function DraftDialog({
  open,
  onOpenChange,
  dirty,
  pending,
  onReset,
  title,
  description,
  discard,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dirty: boolean;
  pending: boolean;
  onReset: () => void;
  title: string;
  description: string;
  discard: string;
  children: ReactNode;
}) {
  const [discarding, setDiscarding] = useState(false);
  const returnFocus = useReturnFocus(open);
  useUnsavedChanges(open && (dirty || pending));
  function close() {
    setDiscarding(false);
    onReset();
    onOpenChange(false);
  }
  return (
    <>
      <Dialog
        onOpenChange={(next) => {
          if (next || pending) return;
          if (dirty) setDiscarding(true);
          else close();
        }}
        open={open}
      >
        <DialogContent onCloseAutoFocus={returnFocus}>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="mt-3 mb-6 text-sm text-muted">
            {description}
          </DialogDescription>
          {children}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        confirmLabel="Discard"
        description={discard}
        onConfirm={close}
        onOpenChange={setDiscarding}
        open={discarding}
        title="Discard your changes?"
      />
    </>
  );
}

function NameDialog({
  open,
  onOpenChange,
  initial = "",
  title,
  description,
  submitLabel,
  mutation,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: string;
  title: string;
  description: string;
  submitLabel: string;
  mutation: ReturnType<typeof useCreateCircle>;
}) {
  const [name, setName] = useState<string | null>(null);
  const value = name ?? initial;
  return (
    <DraftDialog
      description={description}
      dirty={value !== initial}
      discard="The name you entered will not be saved."
      onOpenChange={onOpenChange}
      onReset={() => {
        setName(null);
        mutation.reset();
      }}
      open={open}
      pending={mutation.isPending}
      title={title}
    >
      <Form
        aria-busy={mutation.isPending}
        aria-label={title}
        error={mutation.error}
        onSubmit={(event) => {
          event.preventDefault();
          if (mutation.isPending) return;
          mutation.mutate(
            { name: value },
            {
              onSuccess: () => {
                setName(null);
                mutation.reset();
                onOpenChange(false);
              },
            },
          );
        }}
      >
        <fieldset disabled={mutation.isPending}>
          <Field
            error={fieldErrors(mutation.error).name}
            label="Circle name"
            maxLength={100}
            name="name"
            onChange={(event) => {
              mutation.reset();
              setName(event.target.value);
            }}
            required
            value={value}
          />
          <Button type="submit">
            {mutation.isPending ? "Saving…" : submitLabel}
          </Button>
        </fieldset>
      </Form>
    </DraftDialog>
  );
}

function MembersDialog({
  circle,
  open,
  onOpenChange,
}: {
  circle: Circle;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const people = usePeople("", open);
  const save = useSetCircleMembers(circle.id);
  const initial = circle.members.map((person) => person.id);
  const [draft, setDraft] = useState<string[] | null>(null);
  const [search, setSearch] = useState("");
  const selected = new Set(draft ?? initial);
  const dirty =
    draft !== null &&
    (draft.length !== initial.length ||
      initial.some((id) => !draft.includes(id)));
  const shown = (people.data ?? []).filter((person) =>
    person.display_name.toLowerCase().includes(search.trim().toLowerCase()),
  );
  return (
    <DraftDialog
      description="Tick everyone who belongs in this Circle."
      dirty={dirty}
      discard="Your member choices will not be saved."
      onOpenChange={onOpenChange}
      onReset={() => {
        setDraft(null);
        setSearch("");
        save.reset();
      }}
      open={open}
      pending={save.isPending}
      title={`Members of ${circle.name}`}
    >
      {people.isPending && <p role="status">Loading people…</p>}
      {people.isError && (
        <ReadFailure
          error={people.error}
          pending={people.isFetching}
          retry={people.refetch}
        />
      )}
      {people.data && (
        <Form
          aria-busy={save.isPending}
          aria-label={`Members of ${circle.name}`}
          error={save.error}
          onSubmit={(event) => {
            event.preventDefault();
            if (save.isPending) return;
            save.mutate(
              { person_ids: [...selected] },
              {
                onSuccess: () => {
                  setDraft(null);
                  setSearch("");
                  save.reset();
                  onOpenChange(false);
                },
              },
            );
          }}
        >
          <fieldset disabled={save.isPending}>
            <Input
              aria-label="Search people"
              className="mb-3"
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search people"
              type="search"
              value={search}
            />
            <ul className="max-h-80 overflow-y-auto border-y border-border">
              {shown.map((person) => (
                <li
                  className="border-t border-border first:border-0"
                  key={person.id}
                >
                  <label className="flex cursor-pointer items-center gap-3 px-1 py-2.5 text-sm hover:bg-surface">
                    <input
                      checked={selected.has(person.id)}
                      className="size-4 cursor-pointer accent-primary"
                      onChange={(event) => {
                        save.reset();
                        const next = new Set(selected);
                        if (event.target.checked) next.add(person.id);
                        else next.delete(person.id);
                        setDraft([...next]);
                      }}
                      type="checkbox"
                    />
                    <span aria-hidden="true">
                      <PersonAvatar className="size-7" person={person} />
                    </span>
                    <span className="min-w-0 wrap-anywhere">
                      {person.display_name}
                      {person.deactivated_at && (
                        <span className="text-muted"> (deactivated)</span>
                      )}
                    </span>
                  </label>
                </li>
              ))}
              {shown.length === 0 && (
                <li className="px-1 py-3 text-sm text-muted">
                  {people.data.length
                    ? "No matching people."
                    : "No people yet."}
                </li>
              )}
            </ul>
            <FieldError
              error={fieldErrors(save.error).person_ids}
              id={`circle-${circle.id}-members-error`}
            />
            <p className="mt-3 mb-5 text-xs text-muted">
              {selected.size === 1 ? "1 person" : `${selected.size} people`}{" "}
              selected
            </p>
            <Button type="submit">
              {save.isPending ? "Saving…" : "Save members"}
            </Button>
          </fieldset>
        </Form>
      )}
    </DraftDialog>
  );
}
