import { createHTTP, HTTPError } from "./http";

const origin = "https://photos.example.com";

function reply(status: number, body: string) {
  return { ok: status >= 200 && status < 300, status, text: async () => body };
}

// The real adapter is the one place the app touches the network, so these
// tests stand in for the network itself.
const network = jest.fn();

beforeEach(() => {
  network.mockReset();
  globalThis.fetch = network;
});

afterEach(() => jest.useRealTimers());

it("requests paths on the Installation origin and returns the JSON reply", async () => {
  network.mockResolvedValue(reply(200, '{"claimed":true}'));
  await expect(
    createHTTP(origin).request("/api/identity/status"),
  ).resolves.toEqual({ claimed: true });
  expect(network.mock.calls[0][0]).toBe(`${origin}/api/identity/status`);
  expect(network.mock.calls[0][1].method).toBeUndefined();
});

it("sends a body as a JSON POST", async () => {
  network.mockResolvedValue(reply(204, ""));
  await expect(
    createHTTP(origin).request("/api/example", { body: { name: "Alex" } }),
  ).resolves.toBeUndefined();
  expect(network.mock.calls[0][1]).toMatchObject({
    method: "POST",
    body: '{"name":"Alex"}',
  });
});

it("shows Memento's own message for a request it refused", async () => {
  network.mockResolvedValue(
    reply(
      403,
      '{"error":{"code":"forbidden","message":"Ask a Curator for access."}}',
    ),
  );
  const failure = await createHTTP(origin)
    .request("/api/albums")
    .catch((error: unknown) => error);
  expect(failure).toBeInstanceOf(HTTPError);
  expect(failure).toMatchObject({
    status: 403,
    message: "Ask a Curator for access.",
  });
});

it.each([
  [
    "a server error",
    reply(500, '{"error":{"message":"pq: connection refused"}}'),
  ],
  [
    "a page that is not JSON",
    reply(200, "<!doctype html><title>Router login</title>"),
  ],
])("keeps the details of %s away from the Person", async (_name, response) => {
  network.mockResolvedValue(response);
  await expect(
    createHTTP(origin).request("/api/identity/status"),
  ).rejects.toThrow("Something went wrong. Please try again.");
});

describe("giving up", () => {
  // A network that answers only when the request is aborted.
  function silentNetwork() {
    network.mockImplementation(
      (_url: string, { signal }: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) => {
          const abort = () => reject(new Error("Aborted"));
          if (signal.aborted) abort();
          signal.addEventListener("abort", abort);
        }),
    );
  }

  it("stops waiting for a server that never answers", async () => {
    jest.useFakeTimers();
    silentNetwork();
    const pending = createHTTP(origin).request("/api/identity/status");
    const settled = expect(pending).rejects.toThrow("Aborted");
    await jest.advanceTimersByTimeAsync(10_000);
    await settled;
  });

  it("stops when the caller does, even if the caller already has", async () => {
    silentNetwork();
    const caller = new AbortController();
    const pending = createHTTP(origin).request("/api/identity/status", {
      signal: caller.signal,
    });
    caller.abort();
    await expect(pending).rejects.toThrow("Aborted");

    await expect(
      createHTTP(origin).request("/api/identity/status", {
        signal: caller.signal,
      }),
    ).rejects.toThrow("Aborted");
  });
});
