import { fakeHTTP, notFound } from "@/testing/fake-http";

import { findInstallation } from "./installation";
import { UPDATE_NEEDED } from "./version";

const origin = "https://photos.example.com";
const status = { claimed: true, auth_mode: "google", version: "v1.4.0" };

describe("findInstallation", () => {
  it("returns the Installation and its version when the status endpoint answers", async () => {
    const http = fakeHTTP({ [origin]: { "/api/identity/status": status } });
    await expect(findInstallation(http.create(origin))).resolves.toEqual({
      origin,
      version: "v1.4.0",
    });
    expect(http.requests).toEqual([`${origin}/api/identity/status`]);
  });

  it.each([
    ["nothing answers", {}],
    ["the address serves something else", { "/api/identity/status": notFound }],
    ["the reply is not Memento's", { "/api/identity/status": { ok: true } }],
    ["the reply is not an object", { "/api/identity/status": "<html>" }],
  ])("says Memento is not there when %s", async (_name, replies) => {
    const http = fakeHTTP({ [origin]: replies });
    await expect(findInstallation(http.create(origin))).rejects.toThrow(
      "We couldn't find Memento at that address.",
    );
  });

  it("asks for an update when the Installation is too old", async () => {
    const old = fakeHTTP({
      [origin]: { "/api/identity/status": { ...status, version: "v0.2.0" } },
    });
    await expect(findInstallation(old.create(origin))).rejects.toThrow(
      UPDATE_NEEDED,
    );

    const unversioned = fakeHTTP({
      [origin]: {
        "/api/identity/status": { claimed: true, auth_mode: "google" },
      },
    });
    await expect(findInstallation(unversioned.create(origin))).rejects.toThrow(
      UPDATE_NEEDED,
    );
  });
});
