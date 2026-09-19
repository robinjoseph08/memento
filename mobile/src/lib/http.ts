import type { ErrorResponse } from "@/types/generated/errors";

const GENERIC = "Something went wrong. Please try again.";
const TIMEOUT_MS = 10_000;

export class HTTPError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "HTTPError";
  }
}

// HTTP is the app's only way to reach an Installation. Nothing else calls
// fetch, and tests replace it with fakeHTTP.
export interface HTTP {
  readonly origin: string;
  request<T>(
    path: string,
    options?: { body?: unknown; signal?: AbortSignal },
  ): Promise<T>;
}

export type CreateHTTP = (origin: string) => HTTP;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

// createHTTP binds requests to one Installation origin. Paths are relative to
// it. A request with a body is a JSON POST. A server that never answers fails
// after ten seconds instead of leaving the Person waiting.
export const createHTTP: CreateHTTP = (origin) => ({
  origin,
  async request<T>(
    path: string,
    { body, signal }: { body?: unknown; signal?: AbortSignal } = {},
  ) {
    const timeout = new AbortController();
    const abort = () => timeout.abort();
    const timer = setTimeout(abort, TIMEOUT_MS);
    if (signal?.aborted) {
      abort();
    }
    signal?.addEventListener("abort", abort);
    try {
      const response = await fetch(origin + path, {
        signal: timeout.signal,
        ...(body === undefined
          ? { headers: { Accept: "application/json" } }
          : {
              method: "POST",
              headers: {
                Accept: "application/json",
                "Content-Type": "application/json",
              },
              body: JSON.stringify(body),
            }),
      });
      const text = await response.text();
      let payload: unknown;
      try {
        payload = text ? JSON.parse(text) : undefined;
      } catch {
        throw new HTTPError(GENERIC, response.status);
      }
      if (!response.ok) {
        const error = (payload as Partial<ErrorResponse> | undefined)?.error;
        throw new HTTPError(
          response.status < 500 &&
            isRecord(error) &&
            typeof error.message === "string"
            ? error.message
            : GENERIC,
          response.status,
        );
      }
      return payload as T;
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
    }
  },
});
