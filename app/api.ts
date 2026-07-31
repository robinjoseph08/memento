import type { ProblemResponse } from "./types/generated/errcodes";

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

function isStringMap(value: unknown): value is Record<string, string> {
  return (
    typeof value === "object" &&
    value !== null &&
    !Array.isArray(value) &&
    Object.values(value).every((entry) => typeof entry === "string")
  );
}

function problemMessage(
  payload: unknown,
  responseStatus: number,
): ProblemResponse["error"]["message"] | undefined {
  if (
    typeof payload !== "object" ||
    payload === null ||
    !("error" in payload)
  ) {
    return undefined;
  }
  const { error } = payload;
  if (typeof error !== "object" || error === null) {
    return undefined;
  }
  const fieldErrors = "field_errors" in error ? error.field_errors : undefined;
  const requestID = "request_id" in error ? error.request_id : undefined;
  if (
    !("code" in error) ||
    typeof error.code !== "string" ||
    !error.code ||
    !("message" in error) ||
    typeof error.message !== "string" ||
    !error.message ||
    !("status_code" in error) ||
    typeof error.status_code !== "number" ||
    error.status_code !== responseStatus ||
    (fieldErrors !== undefined && !isStringMap(fieldErrors)) ||
    (requestID !== undefined && typeof requestID !== "string")
  ) {
    return undefined;
  }
  const problem: ProblemResponse = {
    error: {
      code: error.code,
      message: error.message,
      status_code: error.status_code,
      ...(fieldErrors === undefined ? {} : { field_errors: fieldErrors }),
      ...(requestID === undefined ? {} : { request_id: requestID }),
    },
  };
  return problem.error.message;
}

export async function apiResponse(path: string, init?: RequestInit) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (response.ok) {
    return response;
  }
  let message = "Memento is unavailable.";
  try {
    const payload: unknown = await response.json();
    message = problemMessage(payload, response.status) ?? message;
  } catch {
    // The safe fallback does not expose response internals.
  }
  throw new APIError(message, response.status);
}

export async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await apiResponse(path, init);
  return (await response.json()) as T;
}

export async function apiNoContent(
  path: string,
  init: RequestInit,
): Promise<void> {
  await apiResponse(path, init);
}
