import type { Invitation } from "../../types/generated/identity";
import { ConfirmAction } from "../forms/confirm-action";
import { Button } from "../ui/button";
import { invitationLabel } from "./invitation-labels";

export function InvitationStatus({
  invitation,
  retry,
  pending,
  error,
}: {
  invitation: Invitation;
  retry: (invitationID: string) => Promise<unknown>;
  pending: boolean;
  error: unknown;
}) {
  const { delivery } = invitation;
  const failed = delivery.status === "failed";
  const uncertain = delivery.status === "uncertain";
  return (
    <div className="mt-2 text-xs text-muted">
      <p className={failed || uncertain ? "text-destructive" : undefined}>
        {invitationLabel(invitation)}
      </p>
      {delivery.message && <p className="mt-1">{delivery.message}</p>}
      {failed && (
        <Button
          className="mt-2"
          disabled={pending}
          onClick={() => void retry(invitation.id).catch(() => {})}
          size="sm"
          variant="outline"
        >
          {pending ? "Retrying…" : `Retry invitation to ${invitation.email}`}
        </Button>
      )}
      {uncertain && (
        <div className="mt-2">
          <ConfirmAction
            compact
            description={`The mail server may already have accepted this email, so sending it again could deliver a duplicate to ${invitation.email}.`}
            error={error}
            label={`Send invitation to ${invitation.email} again`}
            onConfirm={() => retry(invitation.id)}
            pending={pending}
            triggerLabel="Send again"
          />
        </div>
      )}
    </div>
  );
}
