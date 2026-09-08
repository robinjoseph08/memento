import { formatDate } from "../../lib/utils";
import type { Preauthorization } from "../../types/generated/identity";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";
import { ConfirmAction } from "./confirm-action";

export function PreauthorizationTables({
  authorizations,
  pending,
  error,
  revoke,
}: {
  authorizations: Preauthorization[];
  pending: boolean;
  error: unknown;
  revoke: (id: string) => Promise<unknown>;
}) {
  const active = authorizations.filter(
    (authorization) => !authorization.consumed_at && !authorization.revoked_at,
  );
  const previous = authorizations.filter(
    (authorization) => authorization.consumed_at || authorization.revoked_at,
  );
  return (
    <div className="mt-6">
      {active.length ? (
        <Table
          aria-label="Preauthorizations"
          className="table-fixed sm:table-auto"
        >
          <TableHeader>
            <TableRow>
              <TableHead scope="col">Email</TableHead>
              <TableHead className="hidden sm:table-cell" scope="col">
                Approved
              </TableHead>
              <TableHead className="w-28 sm:w-auto" scope="col">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {active.map((authorization) => (
              <TableRow key={authorization.id}>
                <TableCell className="wrap-anywhere sm:min-w-40">
                  <p>{authorization.email}</p>
                  <p className="mt-2 text-xs text-muted sm:hidden">
                    Approved{" "}
                    <span className="inline-block">
                      {formatDate(authorization.created_at)}
                    </span>
                  </p>
                </TableCell>
                <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                  {formatDate(authorization.created_at)}
                </TableCell>
                <TableCell className="py-2.5 text-right">
                  <ConfirmAction
                    compact
                    description="This unused approval will no longer grant sign-in access."
                    error={error}
                    label={`Revoke ${authorization.email}`}
                    onConfirm={() => revoke(authorization.id)}
                    pending={pending}
                    triggerLabel="Revoke"
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <p className="text-sm text-muted">No unused email approvals.</p>
      )}
      {previous.length > 0 && (
        <details className="mt-5">
          <summary className="cursor-pointer list-inside rounded-sm px-4 py-3 text-sm text-muted hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring">
            Previous emails ({previous.length})
          </summary>
          <div className="mt-3">
            <Table
              aria-label="Previous emails"
              className="table-fixed sm:table-auto"
            >
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Email</TableHead>
                  <TableHead className="w-28 sm:w-auto" scope="col">
                    Status
                  </TableHead>
                  <TableHead className="hidden sm:table-cell" scope="col">
                    Approved
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {previous.map((authorization) => (
                  <TableRow key={authorization.id}>
                    <TableCell className="wrap-anywhere sm:min-w-40">
                      <p>{authorization.email}</p>
                      <p className="mt-2 text-xs text-muted sm:hidden">
                        Approved{" "}
                        <span className="inline-block">
                          {formatDate(authorization.created_at)}
                        </span>
                      </p>
                    </TableCell>
                    <TableCell>
                      {authorization.consumed_at ? "Consumed" : "Revoked"}
                    </TableCell>
                    <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                      {formatDate(authorization.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </details>
      )}
    </div>
  );
}
