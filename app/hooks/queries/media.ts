import { request } from "../../lib/http";
import type { SignedURL, SignRequest } from "../../types/generated/media";

// A TV has no session cookie, so it fetches media through a short-lived
// signed URL. Each one is minted when it is needed and never cached: it
// expires, and the server checks access again on every mint.
export async function signMedia(
  entryID: string,
  variant: "playback" | "preview",
) {
  const body: SignRequest = { entry_id: entryID, variant };
  const signed = await request<SignedURL>("/api/media/signed", { body });
  return signed.url;
}
