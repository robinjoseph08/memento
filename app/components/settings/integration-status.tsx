import {
  CircleAlert,
  CircleCheck,
  CircleDashed,
  RefreshCw,
  type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";

import { errorMessage } from "../../lib/http";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

// Verdict is one integration's state as its section reads it from the API.
// Usable and unusable mirror the backend flag; unconfigured is for optional
// integrations that were never set up, which is quiet rather than a failure.
export type Verdict = {
  tone: "usable" | "unusable" | "unconfigured";
  label: string;
  message: string;
  detail?: string;
  icon?: LucideIcon;
};

const tones = {
  usable: { className: "text-accent-foreground", icon: CircleCheck },
  unusable: { className: "text-destructive", icon: CircleAlert },
  unconfigured: { className: "text-muted", icon: CircleDashed },
};

// One integration's check: the verdict while it is known, an explanation of
// what is configured where, and a manual re-check. The section owns the
// heading and turns its API response into the verdict.
export function IntegrationStatus({
  query,
  checking,
  verdict,
  children,
}: {
  query: {
    isFetching: boolean;
    isError: boolean;
    error: unknown;
    refetch: () => Promise<unknown>;
  };
  checking: string;
  verdict: Verdict | undefined;
  children: ReactNode;
}) {
  return (
    <>
      <div
        aria-live="polite"
        className="mt-5.5 [&>p:first-child]:mb-2 [&>p:first-child]:font-medium"
      >
        {query.isFetching ? (
          <p role="status">{checking}</p>
        ) : query.isError ? (
          <p className="text-destructive" role="alert">
            {errorMessage(query.error)}
          </p>
        ) : (
          verdict && <VerdictLines verdict={verdict} />
        )}
      </div>
      <p className="my-5.5 text-xs/[1.8] text-muted">{children}</p>
      <Button
        disabled={query.isFetching}
        onClick={() => void query.refetch()}
        variant="outline"
      >
        <RefreshCw aria-hidden="true" className="size-4" strokeWidth={1.5} />
        {query.isFetching ? "Checking…" : "Check again"}
      </Button>
    </>
  );
}

function VerdictLines({ verdict }: { verdict: Verdict }) {
  const tone = tones[verdict.tone];
  const Icon = verdict.icon ?? tone.icon;
  return (
    <>
      <p className={cn("flex items-center gap-2", tone.className)}>
        <Icon aria-hidden="true" className="size-4" strokeWidth={1.5} />
        {verdict.label}
      </p>
      <p>{verdict.message}</p>
      {verdict.detail && (
        <p className="mt-2.5 text-[11px]/[1.8] text-muted">{verdict.detail}</p>
      )}
    </>
  );
}
