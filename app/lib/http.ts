import type { ErrorDetail, ErrorResponse } from "../types/generated/errors";

export class HTTPError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly fields: NonNullable<ErrorDetail["fields"]> = {},
  ) {
    super(message);
    this.name = "HTTPError";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

// All API requests use same-origin cookies; mutations send JSON.
export async function request<T>(
  path: string,
  { body, signal }: { body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    signal,
    ...(body === undefined
      ? {}
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        }),
  });
  const text = await response.text();
  let payload: unknown;
  try {
    payload = text ? JSON.parse(text) : undefined;
  } catch {
    throw new HTTPError(
      "Something went wrong. Please try again.",
      response.status,
    );
  }
  if (!response.ok) {
    const envelope = payload as Partial<ErrorResponse> | undefined;
    const error = isRecord(envelope?.error) ? envelope.error : undefined;
    const fields = isRecord(error?.fields)
      ? Object.fromEntries(
          Object.entries(error.fields).filter(
            (entry): entry is [string, string] => typeof entry[1] === "string",
          ),
        )
      : {};
    throw new HTTPError(
      response.status < 500 && typeof error?.message === "string"
        ? error.message
        : "Something went wrong. Please try again.",
      response.status,
      response.status < 500 ? fields : {},
    );
  }
  return payload as T;
}

export function fieldErrors(error: unknown) {
  return error instanceof HTTPError ? error.fields : {};
}

export function errorMessage(error: unknown) {
  return error instanceof HTTPError
    ? error.message
    : "Something went wrong. Please try again.";
}
