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

// MediaSource is what an image component needs to show a photo served by the
// Installation: its absolute address and the headers that authorize it.
export interface MediaSource {
  uri: string;
  headers?: Record<string, string>;
}

// HTTP is the app's only way to reach an Installation. Nothing else calls
// fetch, and tests replace it with fakeHTTP.
export interface HTTP {
  readonly origin: string;
  request<T>(
    path: string,
    options?: { body?: unknown; signal?: AbortSignal },
  ): Promise<T>;
  // media resolves a relative media URL from a payload against the origin.
  media(path: string): MediaSource;
}

export type CreateHTTP = (origin: string, token?: string | null) => HTTP;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

// createHTTP binds requests to one Installation origin, and to the Person's
// session when there is a token, which goes in a bearer header on every
// request and image. Paths are relative to the origin. A request with a body
// is a JSON POST. A server that never answers fails after ten seconds
// instead of leaving the Person waiting.
export const createHTTP: CreateHTTP = (origin, token = null) => {
  const authorization: Record<string, string> = token
    ? { Authorization: `Bearer ${token}` }
    : {};
  return {
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
            ? { headers: { Accept: "application/json", ...authorization } }
            : {
                method: "POST",
                headers: {
                  Accept: "application/json",
                  "Content-Type": "application/json",
                  ...authorization,
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
    media(path) {
      return token
        ? { uri: origin + path, headers: authorization }
        : { uri: origin + path };
    },
  };
};
