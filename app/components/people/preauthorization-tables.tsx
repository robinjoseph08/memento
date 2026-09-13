import { formatDate } from "../../lib/utils";
import type {
  Invitation,
  Preauthorization,
} from "../../types/generated/identity";
import { ConfirmAction } from "../forms/confirm-action";
import { Button } from "../ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";
import { invitationLabel } from "./invitation-labels";
import { InvitationStatus } from "./invitation-status";

export function PreauthorizationTables({
  authorizations,
  invitations,
  pending,
  error,
  revoke,
  invite,
}: {
  authorizations: Preauthorization[];
  invitations: Invitation[];
  pending: boolean;
  error: unknown;
  revoke: (id: string) => Promise<unknown>;
  invite: {
    send: (preauthorizationID: string) => Promise<unknown>;
    retry: (invitationID: string) => Promise<unknown>;
    pending: boolean;
    error: unknown;
  };
}) {
  const invitationFor = (authorization: Preauthorization) =>
    invitations.find(
      (invitation) => invitation.preauthorization_id === authorization.id,
    );
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
            {active.map((authorization) => {
              const invitation = invitationFor(authorization);
              return (
                <TableRow key={authorization.id}>
                  <TableCell className="wrap-anywhere sm:min-w-40">
                    <p>{authorization.email}</p>
                    <p className="mt-2 text-xs text-muted sm:hidden">
                      Approved{" "}
                      <span className="inline-block">
                        {formatDate(authorization.created_at)}
                      </span>
                    </p>
                    {invitation && (
                      <InvitationStatus
                        error={invite.error}
                        invitation={invitation}
                        pending={invite.pending}
                        retry={invite.retry}
                      />
                    )}
                  </TableCell>
                  <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                    {formatDate(authorization.created_at)}
                  </TableCell>
                  <TableCell className="py-2.5 text-right">
                    <div className="flex flex-wrap justify-end gap-2">
                      {!invitation && (
                        <Button
                          aria-label={`Send invitation to ${authorization.email}`}
                          disabled={invite.pending}
                          onClick={() =>
                            void invite.send(authorization.id).catch(() => {})
                          }
                          size="sm"
                          variant="outline"
                        >
                          {invite.pending ? "Sending…" : "Invite"}
                        </Button>
                      )}
                      <ConfirmAction
                        compact
                        description="This unused approval will no longer grant sign-in access."
                        error={error}
                        label={`Revoke ${authorization.email}`}
                        onConfirm={() => revoke(authorization.id)}
                        pending={pending}
                        triggerLabel="Revoke"
                      />
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      ) : (
        <p className="text-sm text-muted">No unused email approvals.</p>
      )}
      {previous.length > 0 && (
        <details className="mt-5">
          <summary className="cursor-pointer list-inside rounded-sm px-4 py-3 text-sm text-muted hover:bg-surface focus-visible:outline-2 focus-visible:outline-ring">
            <span className="ml-2">Previous emails ({previous.length})</span>
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
                      {invitationFor(authorization) && (
                        <p className="mt-1 text-xs text-muted">
                          {invitationLabel(invitationFor(authorization)!)}
                        </p>
                      )}
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
