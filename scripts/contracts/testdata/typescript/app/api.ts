function isSafeProblem(value: unknown): value is { error: string } {
  return (
    typeof value === "object" &&
    value !== null &&
    "error" in value &&
    typeof value.error === "string"
  );
}

export async function apiResponse(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  return fetch(path, init);
}

export async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await apiResponse(path, init);
  const payload: unknown = await response.json();
  if (isSafeProblem(payload)) {
    throw new Error(payload.error);
  }
  return payload as T;
}

export async function apiNoContent(
  path: string,
  init: RequestInit,
): Promise<void> {
  await apiResponse(path, init);
}
