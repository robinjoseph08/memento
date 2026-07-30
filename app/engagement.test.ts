import { afterEach, describe, expect, it, vi } from "vitest";

import { recordEngagement } from "./engagement";
import type { SessionResponse } from "./types/generated/setup";

const session = { csrf_token: "csrf" } as SessionResponse;

afterEach(() => vi.restoreAllMocks());

describe("recordEngagement", () => {
  it("records only explicit actions while the document is visible", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(null, { status: 204 }));

    await recordEngagement(session, {
      kind: "media_opened",
      media_item_id: "8b58c7ca-3a0a-42de-bf91-9d7bfed8c157",
    });

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]?.[0]).toBe("/api/me/engagement");
    const init = fetch.mock.calls[0]?.[1];
    expect(init?.method).toBe("POST");
    expect(init?.keepalive).toBe(true);
    expect(new Headers(init?.headers).get("X-Memento-CSRF")).toBe("csrf");
    const body = init?.body as string;
    expect(body).toContain('"kind":"media_opened"');
    expect(body).toContain('"destination":null');
    expect(body).toContain('"event_id":null');
    expect(body).toContain(
      '"media_item_id":"8b58c7ca-3a0a-42de-bf91-9d7bfed8c157"',
    );
    expect(body).toContain('"document_visible":true');
    expect(body).toMatch(/"client_claim_id":"[0-9a-f-]+"/);
  });

  it("does not report hidden or background work", async () => {
    const fetch = vi.spyOn(globalThis, "fetch");
    await recordEngagement(session, { kind: "visit" }, {
      visibilityState: "hidden",
    } as Document);
    expect(fetch).not.toHaveBeenCalled();
  });
});
