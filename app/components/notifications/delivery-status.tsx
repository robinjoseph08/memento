import { useRetryDelivery } from "../../hooks/queries/notifications";
import { errorMessage } from "../../lib/http";
import { cn } from "../../lib/utils";
import { ConfirmAction } from "../forms/confirm-action";
import { Button } from "../ui/button";
import { deliveryLabel, deliveryNeedsAttention } from "./delivery-labels";

// One email's state with its deliberate retry. A failed email sends again
// directly; an uncertain one asks first, because the server may already have
// accepted it and a second send could deliver a duplicate. Update and alert
// email retry through the delivery record; an Invitation supplies its own
// retry, which also checks that the Person may still be invited, and a
// read-only listing passes false.
export function DeliveryStatus({
  delivery,
  recipient,
  noun = "Email",
  className,
  retry: override,
}: {
  delivery: {
    id?: string;
    status: string;
    message: string;
    delivered_at?: string;
  };
  recipient: string;
  noun?: string;
  className?: string;
  retry?:
    false | { run: () => Promise<unknown>; pending: boolean; error: unknown };
}) {
  const generic = useRetryDelivery();
  const retry =
    override === false
      ? null
      : (override ?? {
          run: () => generic.mutateAsync(delivery.id ?? ""),
          pending: generic.isPending,
          error: generic.error,
        });
  const attention = deliveryNeedsAttention(delivery.status);
  const label = `Send ${noun.toLowerCase()} to ${recipient} again`;
  return (
    <div className={cn("text-xs text-muted", className)}>
      <p className={attention ? "text-destructive" : undefined}>
        {deliveryLabel(delivery, noun)}
        {noun === "Email" &&
          delivery.status !== "delivered" &&
          ` · ${recipient}`}
      </p>
      {delivery.message && <p className="mt-1">{delivery.message}</p>}
      {retry && delivery.status === "failed" && (
        <>
          <Button
            className="mt-2"
            disabled={retry.pending}
            onClick={() => void retry.run().catch(() => {})}
            size="sm"
            variant="outline"
          >
            {retry.pending ? "Sending…" : label}
          </Button>
          {!!retry.error && (
            <p className="mt-2 text-destructive" role="alert">
              {errorMessage(retry.error)}
            </p>
          )}
        </>
      )}
      {retry && delivery.status === "uncertain" && (
        <div className="mt-2">
          <ConfirmAction
            compact
            description={`The mail server may already have accepted this email, so sending it again could deliver a duplicate to ${recipient}.`}
            error={retry.error}
            label={label}
            onConfirm={retry.run}
            pending={retry.pending}
            triggerLabel="Send again"
          />
        </div>
      )}
    </div>
  );
}
