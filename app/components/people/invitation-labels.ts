import { formatDate } from "../../lib/utils";
import type { Invitation } from "../../types/generated/identity";

// Memento's delivery record is the truth shown here. Uncertain delivery needs
// a deliberate choice because sending again may deliver a duplicate email.
export function invitationLabel(invitation: Invitation) {
  const { delivery } = invitation;
  switch (delivery.status) {
    case "queued":
      return "Invitation queued";
    case "sending":
      return "Invitation sending";
    case "delivered":
      return `Invitation sent ${delivery.delivered_at ? formatDate(delivery.delivered_at) : ""}`.trim();
    case "failed":
      return "Invitation not delivered";
    case "uncertain":
      return "Invitation delivery uncertain";
    default:
      return "Invitation status unknown";
  }
}
