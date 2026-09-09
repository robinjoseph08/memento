import { MonitorCheck } from "lucide-react";

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
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";

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
    <Table aria-label="Browser sessions" className="table-fixed md:table-auto">
      <TableHeader>
        <TableRow>
          <TableHead scope="col">Browser</TableHead>
          <TableHead className="hidden md:table-cell" scope="col">
            Signed in
          </TableHead>
          <TableHead className="hidden md:table-cell" scope="col">
            Last used
          </TableHead>
          {showExpiration && (
            <TableHead className="hidden md:table-cell" scope="col">
              Expires
            </TableHead>
          )}
        </TableRow>
      </TableHeader>
      <TableBody>
        {sessions.map((session) => (
          <TableRow key={session.id}>
            <TableCell className="wrap-anywhere md:max-w-64 md:min-w-44">
              <div className="flex items-center gap-2">
                <span>{session.device || "Unknown browser"}</span>
                {session.current && (
                  <>
                    <TooltipProvider>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span
                            aria-label="This browser"
                            className="-my-1 inline-flex size-6 shrink-0 cursor-default items-center justify-center rounded-sm text-accent-foreground focus-visible:outline-2 focus-visible:outline-ring pointer-coarse:hidden"
                            role="img"
                            tabIndex={0}
                          >
                            <MonitorCheck
                              aria-hidden="true"
                              size={16}
                              strokeWidth={1.5}
                            />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>This browser</TooltipContent>
                      </Tooltip>
                    </TooltipProvider>
                    <span className="hidden text-xs whitespace-nowrap text-accent-foreground pointer-coarse:inline">
                      This browser
                    </span>
                  </>
                )}
              </div>
              <p className="mt-2 text-xs text-muted">{session.email}</p>
              <dl className="mt-4 space-y-2 text-xs text-muted md:hidden">
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
            <TableCell className="hidden text-xs whitespace-nowrap text-muted md:table-cell">
              {formatDate(session.created_at)}
            </TableCell>
            <TableCell className="hidden text-xs whitespace-nowrap text-muted md:table-cell">
              {formatDate(session.last_used_at)}
            </TableCell>
            {showExpiration && (
              <TableCell className="hidden text-xs whitespace-nowrap text-muted md:table-cell">
                {formatDate(session.expires_at)}
              </TableCell>
            )}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
