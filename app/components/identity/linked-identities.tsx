import { formatDate } from "../../lib/utils";
import type { LinkedIdentity } from "../../types/generated/identity";
import { ConfirmAction } from "../people/confirm-action";
import { sectionHeadingClass } from "../people/form-fields";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";

export function LinkedIdentities({
  identities,
  canUnlinkLast,
  pending,
  error,
  unlink,
}: {
  identities: LinkedIdentity[];
  canUnlinkLast: boolean;
  pending: boolean;
  error: unknown;
  unlink: (id: string) => Promise<unknown>;
}) {
  const keepLast = !canUnlinkLast && identities.length === 1;
  return (
    <section
      aria-labelledby="linked-accounts"
      className="min-w-0 border-t border-border py-8"
    >
      <h2 className={sectionHeadingClass} id="linked-accounts">
        Linked accounts
      </h2>
      <p className="mt-3 mb-5 max-w-150 text-sm text-muted">
        Unlinking an account signs out all browsers using it.
      </p>
      {identities.length ? (
        <Table
          aria-labelledby="linked-accounts"
          className="table-fixed sm:table-auto"
        >
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Email</TableHead>
              <TableHead className="hidden sm:table-cell" scope="col">
                Provider
              </TableHead>
              <TableHead className="hidden sm:table-cell" scope="col">
                Linked
              </TableHead>
              <TableHead className="w-28 sm:w-auto" scope="col">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {identities.map((identity) => (
              <TableRow key={identity.id}>
                <TableCell className="wrap-anywhere sm:min-w-40">
                  <p>{identity.email}</p>
                  <div className="mt-2 space-y-1 text-xs text-muted sm:hidden">
                    <p>
                      {identity.provider === "google"
                        ? "Google"
                        : "Development"}
                    </p>
                    <p>
                      Linked{" "}
                      <span className="inline-block">
                        {formatDate(identity.created_at)}
                      </span>
                    </p>
                  </div>
                </TableCell>
                <TableCell className="hidden sm:table-cell">
                  {identity.provider === "google" ? "Google" : "Development"}
                </TableCell>
                <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                  {formatDate(identity.created_at)}
                </TableCell>
                <TableCell className="text-right">
                  <ConfirmAction
                    description="This account will no longer be able to sign in, and its browser sessions will end."
                    disabled={keepLast}
                    error={error}
                    label={`Unlink ${identity.email}`}
                    onConfirm={() => unlink(identity.id)}
                    pending={pending}
                    triggerLabel="Unlink"
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <p className="text-sm text-muted">No accounts linked yet.</p>
      )}
      {keepLast && (
        <p className="mt-3 text-xs text-muted">
          Keep at least one linked account so you can sign in.
        </p>
      )}
    </section>
  );
}
