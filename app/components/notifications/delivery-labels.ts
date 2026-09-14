import { formatDate } from "../../lib/utils";

// Memento's delivery record is the truth shown here. Uncertain delivery needs
// a deliberate choice because sending again may deliver a duplicate email.
// The noun names the kind of email: "Email" for updates, "Invitation".
export function deliveryLabel(
  delivery: { status: string; delivered_at?: string },
  noun = "Email",
) {
  switch (delivery.status) {
    case "queued":
      return `${noun} queued`;
    case "sending":
      return `${noun} sending`;
    case "delivered":
      return `${noun} sent ${delivery.delivered_at ? formatDate(delivery.delivered_at) : ""}`.trim();
    case "failed":
      return `${noun} not delivered`;
    case "uncertain":
      return `${noun} delivery uncertain`;
    case "skipped":
      return `${noun} skipped`;
    default:
      return `${noun} status unknown`;
  }
}

export function deliveryNeedsAttention(status: string) {
  return status === "failed" || status === "uncertain";
}
