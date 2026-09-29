import { Link2, Unlink } from "lucide-react";

import { formatDate } from "../../lib/utils";
import type { LinkedEmail } from "../../types/generated/identity";
import { ConfirmAction } from "../forms/confirm-action";
import { SectionHeading } from "../people/form-fields";
import { EmptyState } from "../shell/empty-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";

export function LinkedEmails({
  emails,
  canUnlinkLast,
  pending,
  error,
  unlink,
}: {
  emails: LinkedEmail[];
  canUnlinkLast: boolean;
  pending: boolean;
  error: unknown;
  unlink: (id: string) => Promise<unknown>;
}) {
  const keepLast = !canUnlinkLast && emails.length === 1;
  return (
    <section
      aria-labelledby="linked-emails"
      className="min-w-0 border-t border-border py-8"
    >
      <SectionHeading icon={Link2} id="linked-emails">
        Linked emails
      </SectionHeading>
      <p className="mt-3 mb-5 max-w-150 text-sm text-muted">
        Unlinking an email signs out all browsers using it.
      </p>
      {emails.length ? (
        <Table
          aria-labelledby="linked-emails"
          className="table-fixed sm:table-auto"
        >
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Email</TableHead>
              <TableHead className="hidden sm:table-cell" scope="col">
                Linked
              </TableHead>
              <TableHead className="w-28 sm:w-auto" scope="col">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {emails.map((linked) => (
              <TableRow key={linked.id}>
                <TableCell className="wrap-anywhere sm:min-w-40">
                  <p>{linked.email}</p>
                  <p className="mt-2 text-xs text-muted sm:hidden">
                    Linked{" "}
                    <span className="inline-block">
                      {formatDate(linked.created_at)}
                    </span>
                  </p>
                </TableCell>
                <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                  {formatDate(linked.created_at)}
                </TableCell>
                <TableCell className="py-2.5 text-right">
                  <ConfirmAction
                    compact
                    description="This email will no longer be able to sign in, and its browser sessions will end."
                    disabled={keepLast}
                    error={error}
                    icon={Unlink}
                    label={`Unlink ${linked.email}`}
                    onConfirm={() => unlink(linked.id)}
                    pending={pending}
                    triggerLabel="Unlink"
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <EmptyState icon={Link2}>No emails linked yet.</EmptyState>
      )}
      {keepLast && (
        <p className="mt-3 text-xs text-muted">
          Keep at least one linked email so you can sign in.
        </p>
      )}
    </section>
  );
}
