import { formatDate } from "../../lib/utils";
import type { BrowserSession } from "../../types/generated/identity";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";

export function SessionTable({
  sessions,
  showExpiration,
}: {
  sessions: BrowserSession[];
  showExpiration: boolean;
}) {
  if (!sessions.length)
    return <p className="text-sm text-muted">No active browser sessions.</p>;
  return (
    <Table aria-label="Browser sessions" className="table-fixed sm:table-auto">
      <TableHeader>
        <TableRow>
          <TableHead scope="col">Browser</TableHead>
          <TableHead className="hidden sm:table-cell" scope="col">
            Signed in
          </TableHead>
          <TableHead className="hidden sm:table-cell" scope="col">
            Last used
          </TableHead>
          {showExpiration && (
            <TableHead className="hidden sm:table-cell" scope="col">
              Expires
            </TableHead>
          )}
        </TableRow>
      </TableHeader>
      <TableBody>
        {sessions.map((session) => (
          <TableRow key={session.id}>
            <TableCell className="wrap-anywhere sm:max-w-64 sm:min-w-44">
              <p>{session.device || "Unknown browser"}</p>
              {session.current && (
                <p className="mt-2 text-xs text-accent-foreground">
                  This browser
                </p>
              )}
              <p className="mt-2 text-xs text-muted">{session.email}</p>
              <dl className="mt-4 space-y-2 text-xs text-muted sm:hidden">
                <div className="flex flex-wrap gap-x-2">
                  <dt>Signed in:</dt>
                  <dd>{formatDate(session.created_at)}</dd>
                </div>
                <div className="flex flex-wrap gap-x-2">
                  <dt>Last used:</dt>
                  <dd>{formatDate(session.last_used_at)}</dd>
                </div>
                {showExpiration && (
                  <div className="flex flex-wrap gap-x-2">
                    <dt>Expires:</dt>
                    <dd>{formatDate(session.expires_at)}</dd>
                  </div>
                )}
              </dl>
            </TableCell>
            <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
              {formatDate(session.created_at)}
            </TableCell>
            <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
              {formatDate(session.last_used_at)}
            </TableCell>
            {showExpiration && (
              <TableCell className="hidden text-xs whitespace-nowrap text-muted sm:table-cell">
                {formatDate(session.expires_at)}
              </TableCell>
            )}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
