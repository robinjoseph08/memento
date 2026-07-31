import { afterEach, expect, test, vi } from "vitest";

import { APIError, apiResponse } from "./api";
import type { ProblemResponse } from "./types/generated/errcodes";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

test("uses the generated problem response message", async () => {
  const problem = {
    error: {
      code: "dependency_unavailable",
      message: "The photo library is unavailable.",
      status_code: 502,
    },
  } satisfies ProblemResponse;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(problem), {
          status: 502,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );

  await expect(apiResponse("/api/private")).rejects.toEqual(
    new APIError("The photo library is unavailable.", 502),
  );
});

test("uses the safe fallback for a malformed problem response", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ error: { message: "private upstream details" } }),
          {
            status: 502,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    ),
  );

  await expect(apiResponse("/api/private")).rejects.toEqual(
    new APIError("Memento is unavailable.", 502),
  );
});

test("uses the safe fallback when the problem status does not match", async () => {
  const problem = {
    error: {
      code: "dependency_unavailable",
      message: "private upstream details",
      status_code: 500,
    },
  } satisfies ProblemResponse;
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(problem), {
          status: 502,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    ),
  );

  await expect(apiResponse("/api/private")).rejects.toEqual(
    new APIError("Memento is unavailable.", 502),
  );
});

test.each([
  ["field errors", { field_errors: ["private upstream details"] }],
  ["request ID", { request_id: { private: "upstream details" } }],
])("uses the safe fallback for malformed %s", async (_name, optionalFields) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            error: {
              code: "dependency_unavailable",
              message: "private upstream details",
              status_code: 502,
              ...optionalFields,
            },
          }),
          {
            status: 502,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    ),
  );

  await expect(apiResponse("/api/private")).rejects.toEqual(
    new APIError("Memento is unavailable.", 502),
  );
});

test("does not expose a non-JSON error response", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response("private upstream details", {
          status: 503,
          headers: { "Content-Type": "text/plain" },
        }),
      ),
    ),
  );

  await expect(apiResponse("/api/private")).rejects.toEqual(
    new APIError("Memento is unavailable.", 503),
  );
});
