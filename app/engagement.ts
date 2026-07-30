import { apiNoContent } from "./api";
import type { BrowserEventRequest } from "./types/generated/engagement";
import type { SessionResponse } from "./types/generated/setup";

type ExplicitEngagement = Pick<BrowserEventRequest, "kind"> &
  Partial<
    Pick<BrowserEventRequest, "destination" | "event_id" | "media_item_id">
  >;

// Engagement is best-effort and only follows deliberate visible-document actions.
// Protected GETs, image loads, prefetches, and service-worker traffic never call this boundary.
export async function recordEngagement(
  session: SessionResponse,
  event: ExplicitEngagement,
  currentDocument: Document = document,
): Promise<void> {
  if (currentDocument.visibilityState !== "visible") return;
  const request: BrowserEventRequest = {
    kind: event.kind,
    client_claim_id: crypto.randomUUID(),
    destination: event.destination ?? null,
    event_id: event.event_id ?? null,
    media_item_id: event.media_item_id ?? null,
    document_visible: true,
  };
  try {
    await apiNoContent("/api/me/engagement", {
      method: "POST",
      keepalive: true,
      headers: { "X-Memento-CSRF": session.csrf_token },
      body: JSON.stringify(request),
    });
  } catch {
    // Tracking must never block access, navigation, playback, or downloads.
  }
}
